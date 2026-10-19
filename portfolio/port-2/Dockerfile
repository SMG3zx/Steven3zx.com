FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/portfolio .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/portfolio ./portfolio
COPY assets ./assets
EXPOSE 8090
ENTRYPOINT ["/app/portfolio"]
