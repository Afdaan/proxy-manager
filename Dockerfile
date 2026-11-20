FROM node:24-alpine AS web-build

WORKDIR /src
RUN npm install --global pnpm@12.21.0
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json apps/web/package.json
RUN pnpm install --frozen-lockfile
COPY apps/web apps/web
RUN pnpm --dir apps/web build

FROM golang:1.27-alpine AS server-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY db db
COPY internal internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy-manager-server ./cmd/server \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy-manager-agent ./cmd/agent

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 proxy-manager \
    && adduser -S -D -H -u 10001 -G proxy-manager proxy-manager

COPY --from=server-build /out/proxy-manager-server /usr/local/bin/proxy-manager-server
COPY --from=web-build /src/apps/web/dist /opt/proxy-manager/web

USER 10001:10001
ENV PROXY_MANAGER_HTTP_ADDRESS=0.0.0.0:8080
ENV PROXY_MANAGER_WEB_ROOT=/opt/proxy-manager/web
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/proxy-manager-server"]
