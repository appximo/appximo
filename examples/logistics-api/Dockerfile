FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/appximo ./cmd/appximo

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /usr/local/bin/appximo /usr/local/bin/appximo
COPY examples/logistics-api/schema.json /app/schema.json
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["appximo"]
CMD ["serve", "--schema", "/app/schema.json", "--port", "8080"]
