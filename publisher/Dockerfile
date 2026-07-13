FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /account-publisher ./cmd/account-publisher

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /account-publisher /account-publisher
EXPOSE 8080
ENTRYPOINT ["/account-publisher"]
