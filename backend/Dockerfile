FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/cornice ./cmd/cornice

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/cornice /cornice
ENTRYPOINT ["/cornice"]
CMD ["tick"]
