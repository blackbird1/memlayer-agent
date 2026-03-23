FROM golang:1.25 AS builder

WORKDIR /app

# Context is now root, so we copy from go/
COPY go/go.mod go/go.sum ./
RUN go mod download

COPY go/ .

RUN CGO_ENABLED=0 GOOS=linux go build -o /adk-server ./cmd/adk-server

FROM gcr.io/distroless/base-debian12

WORKDIR /

COPY --from=builder /adk-server /adk-server

EXPOSE 9090

ENTRYPOINT ["/adk-server"]
