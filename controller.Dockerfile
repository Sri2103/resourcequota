# build stage
FROM golang:1.24-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/rqe ./cmd

# final image
FROM alpine:3.18
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/rqe /bin/rqe
USER 65532:65532
ENTRYPOINT ["/bin/rqe"]
