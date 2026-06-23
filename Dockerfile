# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION is baked into the binary and reported as the service.version
# trace attribute. Build with: docker build --build-arg VERSION=0.0.4 .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" -o /pwgen-api .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pwgen-api /pwgen-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/pwgen-api"]
