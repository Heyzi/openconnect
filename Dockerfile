FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /litellm-config-adapter ./cmd/litellm-config-adapter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /litellm-config-adapter /litellm-config-adapter
ENTRYPOINT ["/litellm-config-adapter"]
