FROM node:24-alpine AS web-build
WORKDIR /src/web
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.25-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mijia-archive ./cmd/mijia-archive

FROM alpine:3.22
RUN apk add --no-cache ca-certificates ffmpeg tzdata && addgroup -g 10001 app && adduser -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=go-build /out/mijia-archive /usr/local/bin/mijia-archive
COPY --from=web-build /src/web/dist /app/web
USER app:app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/mijia-archive"]
CMD ["serve", "--web-dir", "/app/web", "--listen", "0.0.0.0:8080"]
