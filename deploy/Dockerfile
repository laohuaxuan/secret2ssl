FROM acr-openxlab-prod-registry-vpc.cn-shanghai.cr.aliyuncs.com/public/golang:1.25.6 AS builder
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR $GOPATH/src
COPY . .
RUN go env -w GOPROXY=https://mirrors.aliyun.com/goproxy/,direct && go build -o /app/ack-vpa-updater
    
FROM acr-openxlab-prod-registry-vpc.cn-shanghai.cr.aliyuncs.com/public/alpine:latest
WORKDIR /app

COPY --from=builder /app .

RUN apk add --no-cache tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

ENTRYPOINT ["./secret2ssl"]