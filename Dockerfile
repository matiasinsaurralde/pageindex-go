# Build stage
FROM golang:1.24-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /pageindex ./cmd/pageindex

# Run stage
FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /pageindex /usr/local/bin/pageindex

EXPOSE 8080
ENTRYPOINT ["pageindex", "--serve", "--listen", ":8080"]
