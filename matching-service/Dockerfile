FROM golang:1.21-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /workspace
ENV GONOSUMCHECK=* GONOSUMDB=*
COPY go.work go.work
COPY shared/ shared/
COPY user-service/ user-service/
COPY driver-service/ driver-service/
COPY trip-service/ trip-service/
COPY matching-service/ matching-service/
COPY notification-service/ notification-service/
COPY payment-service/ payment-service/
WORKDIR /workspace/matching-service
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /matching-service ./cmd

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /matching-service .
CMD ["./matching-service"]
