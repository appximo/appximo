FROM golang:alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o appitools ./cmd/appitools

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/appitools .
COPY --from=builder /app/testdata/logistics/schema.json ./schema.json
EXPOSE 8080
CMD ["./appitools", "serve", "--schema", "schema.json"]
