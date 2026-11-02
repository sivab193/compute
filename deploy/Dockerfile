# Multi-stage build for the control plane. Pure-Go SQLite (modernc) means no
# CGO, so the final image is a tiny static binary on a distroless base.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o /out/compute-server ./cmd/compute-server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/compute-server /compute-server
EXPOSE 8080
ENTRYPOINT ["/compute-server"]
