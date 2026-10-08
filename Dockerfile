# Multi-stage Dockerfile for MacOCR Platform (Proxy + Embedded Admin UI & Docs)

# Define global build arguments
ARG PUBLIC_API_BASE_URL=https://ocr.dungxbuif.com
ARG PUBLIC_DOCS_BASE_URL=https://ocr.dungxbuif.com
ARG APP_ENV=production
ARG VERSION=1.0.0
ARG GIT_COMMIT=unknown

# Stage 1: Build Admin UI (React + Vite)
FROM node:20-alpine AS admin-builder
ARG PUBLIC_API_BASE_URL
ARG PUBLIC_DOCS_BASE_URL
ENV VITE_PUBLIC_API_BASE_URL=${PUBLIC_API_BASE_URL} \
    VITE_PUBLIC_DOCS_BASE_URL=${PUBLIC_DOCS_BASE_URL}

WORKDIR /app/proxy/admin-ui
COPY proxy/admin-ui/package*.json ./
RUN npm ci
COPY proxy/admin-ui/ ./
RUN npm run build

# Stage 2: Build Docs Site (Docusaurus)
FROM node:20-alpine AS docs-builder
ARG PUBLIC_API_BASE_URL
ARG PUBLIC_DOCS_BASE_URL
ENV PUBLIC_API_BASE_URL=${PUBLIC_API_BASE_URL} \
    PUBLIC_DOCS_BASE_URL=${PUBLIC_DOCS_BASE_URL}

WORKDIR /app/docs-site
COPY docs-site/package*.json ./
RUN npm ci
COPY docs-site/ ./
COPY docs/ /app/docs/
RUN npm run build

# Stage 3: Build the pinned Firecrawl PDF text extractor. The default crate
# build is text-only, so it adds no OCR models or native inference runtime.
FROM rust:1.98-alpine AS pdf-inspector-builder
RUN apk add --no-cache git musl-dev
WORKDIR /src/pdf-inspector
RUN git clone --depth 1 --branch v1.25.2 https://github.com/firecrawl/pdf-inspector .
RUN cargo build --release --bin pdf2md

# Stage 4: Build Go Proxy & Admin CLI
FROM golang:1.26.6-alpine AS go-builder
RUN apk add --no-cache ca-certificates git

ARG VERSION
ARG GIT_COMMIT

WORKDIR /app/proxy

# Cache Go dependencies
COPY proxy/go.mod proxy/go.sum ./
RUN go mod download

# Copy proxy source code
COPY proxy/ ./

# Copy compiled static assets into Go embed target paths
COPY --from=admin-builder /app/proxy/admin/static ./admin/static
COPY --from=docs-builder /app/proxy/docs/static ./docs/static

# Build static Linux CGO-disabled binaries
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" \
    -o /app/bin/macocr-proxy ./cmd/proxy

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" \
    -o /app/bin/macocr-admin ./cmd/admin

# Stage 5: Minimal Runtime Image
FROM alpine:3.20 AS runner
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S macocr \
    && adduser -S -G macocr -H -s /sbin/nologin macocr

ARG APP_ENV
ARG PUBLIC_API_BASE_URL
ARG PUBLIC_DOCS_BASE_URL

WORKDIR /app

COPY --from=go-builder /app/bin/macocr-proxy /usr/local/bin/macocr-proxy
COPY --from=go-builder /app/bin/macocr-admin /usr/local/bin/macocr-admin
COPY --from=pdf-inspector-builder /src/pdf-inspector/target/release/pdf2md /usr/local/bin/pdf2md

EXPOSE 8080

ENV APP_ENV=${APP_ENV} \
    HTTP_ADDR=:8080 \
    PUBLIC_API_BASE_URL=${PUBLIC_API_BASE_URL} \
    PUBLIC_DOCS_BASE_URL=${PUBLIC_DOCS_BASE_URL}

USER macocr

ENTRYPOINT ["macocr-proxy"]
