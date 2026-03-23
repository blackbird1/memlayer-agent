package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	url := "http://host.docker.internal/mcp"
	token := os.Getenv("MEMLAYER_MCP_BEARER_TOKEN")

	fmt.Printf("Connecting to %s\n", url)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("Status: %s\n", resp.Status)
	fmt.Printf("Headers: %v\n", resp.Header)

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Stream ended or error: %v\n", err)
			return
		}
		line = strings.TrimSpace(line)
		if line != "" {
			fmt.Printf("LINE: %s\n", line)
		}
	}
}
