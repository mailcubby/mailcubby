# Stage 1: web
FROM node:26-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@12.21.0
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# Stage 2: go
FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ internal/web/dist/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /out/mailcubby ./cmd/mailcubby \
 && mkdir -p /out/data

# Stage 3: runtime
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/mailcubby /mailcubby
COPY --from=build --chown=65532:65532 /out/data /data
ENV MAILCUBBY_DATA_DIR=/data
VOLUME /data
EXPOSE 1025 8025
HEALTHCHECK --interval=30s --timeout=3s CMD ["/mailcubby", "healthcheck"]
ENTRYPOINT ["/mailcubby"]
CMD ["serve"]
