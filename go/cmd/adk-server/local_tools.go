package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"google.golang.org/genai"
)

const finnhubBaseURL = "https://finnhub.io/api/v1"

type finnhubSearchResult struct {
	Count  int `json:"count"`
	Result []struct {
		Description   string `json:"description"`
		DisplaySymbol string `json:"displaySymbol"`
		Symbol        string `json:"symbol"`
		Type          string `json:"type"`
	} `json:"result"`
}

type finnhubQuote struct {
	CurrentPrice  float64 `json:"c"`
	Change        float64 `json:"d"`
	PercentChange float64 `json:"dp"`
	High          float64 `json:"h"`
	Low           float64 `json:"l"`
	Open          float64 `json:"o"`
	PrevClose     float64 `json:"pc"`
	Timestamp     int64   `json:"t"`
}

func listLocalToolNames() []string {
	return []string{"finnhub_search_tickers", "finnhub_get_quote"}
}

func buildLocalToolset() ([]*genai.FunctionDeclaration, map[string]ToolExecutor) {
	decls := []*genai.FunctionDeclaration{
		{
			Name:        "finnhub_search_tickers",
			Description: "Search for stock tickers by company name or symbol using Finnhub.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"query": {
						Type:        genai.TypeString,
						Description: "The company name or ticker symbol to search for.",
					},
				},
				Required: []string{"query"},
			},
		},
	}

	decls = append(decls, &genai.FunctionDeclaration{
		Name:        "finnhub_get_quote",
		Description: "Fetch the current stock price and quote data for a ticker symbol using Finnhub.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"symbol": {
					Type:        genai.TypeString,
					Description: "The ticker symbol to fetch a quote for (e.g. AAPL, MSFT).",
				},
			},
			Required: []string{"symbol"},
		},
	})

	executors := map[string]ToolExecutor{
		"finnhub_search_tickers": executeFinnhubSearchTickers,
		"finnhub_get_quote":      executeFinnhubGetQuote,
	}

	return decls, executors
}

func executeFinnhubGetQuote(ctx context.Context, args map[string]any) (string, error) {
	symbol, _ := args["symbol"].(string)
	if symbol == "" {
		return "", fmt.Errorf("symbol is required")
	}

	apiKey := os.Getenv("FINNHUB_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("FINNHUB_API_KEY is not set")
	}

	endpoint := fmt.Sprintf("%s/quote?symbol=%s&token=%s", finnhubBaseURL, url.QueryEscape(symbol), url.QueryEscape(apiKey))

	raw, err := doJSONRequest(ctx, &http.Client{}, "GET", endpoint, nil, nil)
	if err != nil {
		return "", fmt.Errorf("finnhub quote failed: %w", err)
	}

	var quote finnhubQuote
	if err := json.Unmarshal(raw, &quote); err != nil {
		return "", fmt.Errorf("failed to parse finnhub response: %w", err)
	}

	result := fmt.Sprintf(
		"%s: $%.2f (change: %.2f, %.2f%%) | Open: $%.2f | High: $%.2f | Low: $%.2f | Prev Close: $%.2f",
		symbol,
		quote.CurrentPrice,
		quote.Change,
		quote.PercentChange,
		quote.Open,
		quote.High,
		quote.Low,
		quote.PrevClose,
	)
	return result, nil
}

func executeFinnhubSearchTickers(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	apiKey := os.Getenv("FINNHUB_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("FINNHUB_API_KEY is not set")
	}

	endpoint := fmt.Sprintf("%s/search?q=%s&token=%s", finnhubBaseURL, url.QueryEscape(query), url.QueryEscape(apiKey))

	raw, err := doJSONRequest(ctx, &http.Client{}, "GET", endpoint, nil, nil)
	if err != nil {
		return "", fmt.Errorf("finnhub search failed: %w", err)
	}

	var result finnhubSearchResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("failed to parse finnhub response: %w", err)
	}

	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
