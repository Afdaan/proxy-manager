package agent

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/afdaan/proxy-manager/internal/model"
)

type HTTPHandler struct {
	Token        string
	Applier      *Applier
	Certificates CertificateManager
}

func (handler HTTPHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", handler.health)
	mux.HandleFunc("POST /v1/deployments/validate", handler.authorize(handler.validate))
	mux.HandleFunc("POST /v1/deployments/apply", handler.authorize(handler.apply))
	mux.HandleFunc("POST /v1/deployments/rollback", handler.authorize(handler.rollback))
	mux.HandleFunc("POST /v1/certificates/issue", handler.authorize(handler.issueCertificate))
	return mux
}

func (handler HTTPHandler) issueCertificate(response http.ResponseWriter, request *http.Request) {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	var certificateRequest CertificateRequest
	if err := decoder.Decode(&certificateRequest); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if err := handler.Certificates.Issue(request.Context(), certificateRequest); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "issued"})
}

func (handler HTTPHandler) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok", "service": "agent"})
}

func (handler HTTPHandler) validate(response http.ResponseWriter, request *http.Request) {
	config, err := decodeGatewayConfig(response, request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if _, err := handler.Applier.Renderer.Render(config); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "valid", "revision": config.Revision})
}

func (handler HTTPHandler) apply(response http.ResponseWriter, request *http.Request) {
	config, err := decodeGatewayConfig(response, request)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	if err := handler.Applier.Apply(request.Context(), config); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "applied", "revision": config.Revision})
}

func (handler HTTPHandler) rollback(response http.ResponseWriter, request *http.Request) {
	if err := handler.Applier.Rollback(request.Context()); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "rolled_back"})
}

func (handler HTTPHandler) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if handler.Token == "" || len(provided) != len(handler.Token) || subtle.ConstantTimeCompare([]byte(provided), []byte(handler.Token)) != 1 {
			writeError(response, http.StatusUnauthorized, errors.New("invalid agent token"))
			return
		}
		next(response, request)
	}
}

func decodeGatewayConfig(response http.ResponseWriter, request *http.Request) (model.GatewayConfig, error) {
	defer request.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var config model.GatewayConfig
	if err := decoder.Decode(&config); err != nil {
		return model.GatewayConfig{}, err
	}
	return config, nil
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
