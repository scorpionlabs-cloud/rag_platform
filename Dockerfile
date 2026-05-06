FROM golang:1.21
RUN apt-get update && apt-get install -y tesseract-ocr poppler-utils && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY . .
RUN go mod tidy
RUN go build -o api ./cmd/api
CMD ["./api"]
