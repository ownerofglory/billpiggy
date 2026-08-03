# Build phase
FROM golang:1.24.1 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG BUILD_VERSION=dev
ENV CGO_ENABLED=0
RUN make build VERSION=$BUILD_VERSION

# Run phase
FROM alpine:3.22

RUN addgroup -S -g 10001 billpiggy && adduser -S -D -u 10001 -G billpiggy billpiggy

COPY --from=build /app/bin/billpiggy /usr/local/bin/billpiggy
USER billpiggy
EXPOSE 8080

CMD ["billpiggy"]
