FROM node:24-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@11.25.0
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS server
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kaspi-pos ./cmd/kaspi-pos

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 kaspi
WORKDIR /app
COPY --from=server /out/kaspi-pos /app/kaspi-pos
COPY --from=web /src/web/dist /app/web/dist
RUN mkdir /data && chown kaspi:kaspi /data
USER kaspi
ENV HOST=0.0.0.0 PORT=3000 STATE_DIR=/data FRONTEND_DIR=/app/web/dist
VOLUME /data
EXPOSE 3000
CMD ["/app/kaspi-pos"]
