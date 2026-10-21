FROM node:22.22.0-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/hub/static ./internal/hub/static
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/rootlens ./cmd/rootlens && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/rootlens-agent ./cmd/rootlens-agent

FROM alpine:3.23.2 AS hub
RUN addgroup -g 10001 rootlens && adduser -D -u 10001 -G rootlens rootlens && mkdir /data && chown rootlens:rootlens /data
COPY --from=build /out/rootlens /usr/local/bin/rootlens
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/rootlens/
COPY docs/licenses /usr/share/licenses/rootlens/dependencies/
USER 10001:10001
WORKDIR /data
EXPOSE 8443
ENTRYPOINT ["rootlens"]
CMD ["serve", "--data", "/data", "--listen", "0.0.0.0:8443"]

FROM alpine:3.23.2 AS agent
RUN addgroup -g 10001 rootlens && adduser -D -u 10001 -G rootlens rootlens && mkdir /state && chown rootlens:rootlens /state
COPY --from=build /out/rootlens-agent /usr/local/bin/rootlens-agent
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/rootlens/
COPY docs/licenses /usr/share/licenses/rootlens/dependencies/
USER 10001:10001
ENTRYPOINT ["rootlens-agent"]
CMD ["--config", "/etc/rootlens/agent.json"]
