FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/example-service .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/example-service /example-service
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/example-service"]
