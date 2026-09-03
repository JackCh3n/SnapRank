package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	key := os.Getenv("ARK_KEY")
	if key == "" {
		fmt.Println("需要 ARK_KEY 环境变量")
		return
	}
	url := "https://ark.cn-beijing.volces.com/api/coding/v1/models"

	// HTTP/1.1（空 Transport 不启用 HTTP/2）
	h1 := &http.Client{Transport: &http.Transport{}}
	req1, _ := http.NewRequest("GET", url, nil)
	req1.Header.Set("Authorization", "Bearer "+key)
	req1.Header.Set("anthropic-version", "2023-06-01")
	r1, err1 := h1.Do(req1)
	if err1 != nil {
		fmt.Println("h1 err:", err1)
	} else {
		b1, _ := io.ReadAll(io.LimitReader(r1.Body, 60))
		fmt.Println("h1:", r1.Status, string(b1)[:60])
		r1.Body.Close()
	}

	// 默认客户端（启用 HTTP/2）
	req2, _ := http.NewRequest("GET", url, nil)
	req2.Header.Set("Authorization", "Bearer "+key)
	req2.Header.Set("anthropic-version", "2023-06-01")
	r2, err2 := http.DefaultClient.Do(req2)
	if err2 != nil {
		fmt.Println("h2 err:", err2)
		return
	}
	b2, _ := io.ReadAll(io.LimitReader(r2.Body, 200))
	fmt.Println("h2:", r2.Status, r2.Proto, string(b2)[:120])
	r2.Body.Close()
}
