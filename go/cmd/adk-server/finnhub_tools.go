// Example tool integration: Finnhub stock market data.
// This file shows how to add local (non-MCP) tools to the agent.
// Copy this pattern to integrate any REST API as a local tool.
// Loaded automatically when FINNHUB_API_KEY is set in the environment.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const finnhubBaseURL = "https://finnhub.io/api/v1"

// --- Types ---

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
}

type finnhubProfile struct {
	Name          string  `json:"name"`
	Ticker        string  `json:"ticker"`
	Exchange      string  `json:"exchange"`
	Industry      string  `json:"finnhubIndustry"`
	IPO           string  `json:"ipo"`
	MarketCap     float64 `json:"marketCapitalization"`
	SharesOut     float64 `json:"shareOutstanding"`
	Currency      string  `json:"currency"`
	Country       string  `json:"country"`
	WebURL        string  `json:"weburl"`
}

type finnhubNewsItem struct {
	Headline string `json:"headline"`
	Source   string `json:"source"`
	Summary  string `json:"summary"`
	URL      string `json:"url"`
	Datetime int64  `json:"datetime"`
}

type finnhubEarningsCalendar struct {
	EarningsCalendar []struct {
		Symbol          string  `json:"symbol"`
		Date            string  `json:"date"`
		Hour            string  `json:"hour"`
		Quarter         int     `json:"quarter"`
		Year            int     `json:"year"`
		EPSEstimate     float64 `json:"epsEstimate"`
		EPSActual       float64 `json:"epsActual"`
		RevenueEstimate float64 `json:"revenueEstimate"`
		RevenueActual   float64 `json:"revenueActual"`
	} `json:"earningsCalendar"`
}

type finnhubRecommendation struct {
	Period    string `json:"period"`
	StrongBuy int    `json:"strongBuy"`
	Buy       int    `json:"buy"`
	Hold      int    `json:"hold"`
	Sell      int    `json:"sell"`
	StrongSell int   `json:"strongSell"`
}

type finnhubInsiderSentiment struct {
	Symbol string `json:"symbol"`
	Data   []struct {
		Year   int     `json:"year"`
		Month  int     `json:"month"`
		Change int     `json:"change"`
		MSPR   float64 `json:"mspr"`
	} `json:"data"`
}

type finnhubPriceTarget struct {
	Symbol      string  `json:"symbol"`
	TargetHigh  float64 `json:"targetHigh"`
	TargetLow   float64 `json:"targetLow"`
	TargetMean  float64 `json:"targetMean"`
	TargetMedian float64 `json:"targetMedian"`
	LastUpdated string  `json:"lastUpdated"`
}

// --- Helpers ---

func finnhubAPIKey() (string, error) {
	key := os.Getenv("FINNHUB_API_KEY")
	if key == "" {
		return "", fmt.Errorf("FINNHUB_API_KEY is not set")
	}
	return key, nil
}

func finnhubGET(ctx context.Context, path string) ([]byte, error) {
	key, err := finnhubAPIKey()
	if err != nil {
		return nil, err
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	endpoint := finnhubBaseURL + path + sep + "token=" + url.QueryEscape(key)
	return doJSONRequest(ctx, &http.Client{}, "GET", endpoint, nil, nil)
}

func requireSymbol(args map[string]any) (string, error) {
	sym, _ := args["symbol"].(string)
	if sym == "" {
		return "", fmt.Errorf("symbol is required")
	}
	return sym, nil
}

// --- Tool Registry ---


func symbolParams() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "Ticker symbol (e.g. AAPL, MSFT).",
			},
		},
		"required": []string{"symbol"},
	}
}

func buildFinnhubToolset() ([]openai.Tool, map[string]ToolExecutor) {
	tool := func(name, description string, params map[string]any) openai.Tool {
		return openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: description,
				Parameters:  params,
			},
		}
	}

	tools := []openai.Tool{
		tool("finnhub_search_tickers", "Search for stock tickers by company name or symbol.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Company name or ticker symbol to search for.",
				},
			},
			"required": []string{"query"},
		}),
		tool("finnhub_get_quote", "Fetch the current stock price and intraday quote data.", symbolParams()),
		tool("finnhub_get_company_profile", "Fetch company profile: name, exchange, industry, market cap, IPO date, website.", symbolParams()),
		tool("finnhub_get_company_news", "Fetch the latest company news headlines (last 7 days, up to 5 articles).", symbolParams()),
		tool("finnhub_get_earnings_calendar", "Fetch upcoming or recent earnings dates and EPS/revenue estimates for a symbol (next 90 days).", symbolParams()),
		tool("finnhub_get_analyst_recommendations", "Fetch the latest analyst buy/hold/sell recommendation consensus.", symbolParams()),
		tool("finnhub_get_insider_sentiment", "Fetch insider trading sentiment (MSPR score and net share change) for the last 3 months.", symbolParams()),
		tool("finnhub_get_price_target", "Fetch analyst consensus price target: high, low, mean, and median.", symbolParams()),
	}

	executors := map[string]ToolExecutor{
		"finnhub_search_tickers":              executeFinnhubSearchTickers,
		"finnhub_get_quote":                   executeFinnhubGetQuote,
		"finnhub_get_company_profile":         executeFinnhubGetCompanyProfile,
		"finnhub_get_company_news":            executeFinnhubGetCompanyNews,
		"finnhub_get_earnings_calendar":       executeFinnhubGetEarningsCalendar,
		"finnhub_get_analyst_recommendations": executeFinnhubGetAnalystRecommendations,
		"finnhub_get_insider_sentiment":       executeFinnhubGetInsiderSentiment,
		"finnhub_get_price_target":            executeFinnhubGetPriceTarget,
	}

	return tools, executors
}

// --- Executors ---

func executeFinnhubSearchTickers(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	logger.Info("finnhub search tickers", "tool", "finnhub_search_tickers", "query", query)
	raw, err := finnhubGET(ctx, "/search?q="+url.QueryEscape(query))
	if err != nil {
		logger.Error("finnhub search failed", "tool", "finnhub_search_tickers", "query", query, "error", err)
		return "", fmt.Errorf("finnhub search failed: %w", err)
	}
	var result finnhubSearchResult
	if err := json.Unmarshal(raw, &result); err != nil {
		logger.Error("finnhub search parse failed", "tool", "finnhub_search_tickers", "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub search tickers completed", "tool", "finnhub_search_tickers", "query", query, "count", result.Count)
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func executeFinnhubGetQuote(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	logger.Info("finnhub get quote", "tool", "finnhub_get_quote", "symbol", symbol)
	raw, err := finnhubGET(ctx, "/quote?symbol="+url.QueryEscape(symbol))
	if err != nil {
		logger.Error("finnhub quote failed", "tool", "finnhub_get_quote", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub quote failed: %w", err)
	}
	var q finnhubQuote
	if err := json.Unmarshal(raw, &q); err != nil {
		logger.Error("finnhub quote parse failed", "tool", "finnhub_get_quote", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get quote completed", "tool", "finnhub_get_quote", "symbol", symbol, "price", q.CurrentPrice)
	return fmt.Sprintf(
		"%s: $%.2f (change: %.2f, %.2f%%) | Open: $%.2f | High: $%.2f | Low: $%.2f | Prev Close: $%.2f",
		symbol, q.CurrentPrice, q.Change, q.PercentChange, q.Open, q.High, q.Low, q.PrevClose,
	), nil
}

func executeFinnhubGetCompanyProfile(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	logger.Info("finnhub get company profile", "tool", "finnhub_get_company_profile", "symbol", symbol)
	raw, err := finnhubGET(ctx, "/stock/profile2?symbol="+url.QueryEscape(symbol))
	if err != nil {
		logger.Error("finnhub profile failed", "tool", "finnhub_get_company_profile", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub profile failed: %w", err)
	}
	var p finnhubProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		logger.Error("finnhub profile parse failed", "tool", "finnhub_get_company_profile", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get company profile completed", "tool", "finnhub_get_company_profile", "symbol", symbol, "name", p.Name)
	return fmt.Sprintf(
		"%s (%s) | Exchange: %s | Industry: %s | IPO: %s | Market Cap: $%.2fB | Shares Out: %.2fM | Currency: %s | Country: %s | Web: %s",
		p.Name, p.Ticker, p.Exchange, p.Industry, p.IPO,
		p.MarketCap/1000, p.SharesOut, p.Currency, p.Country, p.WebURL,
	), nil
}

func executeFinnhubGetCompanyNews(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	to := time.Now().Format("2006-01-02")
	from := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	logger.Info("finnhub get company news", "tool", "finnhub_get_company_news", "symbol", symbol, "from", from, "to", to)
	path := fmt.Sprintf("/company-news?symbol=%s&from=%s&to=%s", url.QueryEscape(symbol), from, to)
	raw, err := finnhubGET(ctx, path)
	if err != nil {
		logger.Error("finnhub news failed", "tool", "finnhub_get_company_news", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub news failed: %w", err)
	}
	var items []finnhubNewsItem
	if err := json.Unmarshal(raw, &items); err != nil {
		logger.Error("finnhub news parse failed", "tool", "finnhub_get_company_news", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get company news completed", "tool", "finnhub_get_company_news", "symbol", symbol, "articles", len(items))
	if len(items) == 0 {
		return fmt.Sprintf("No news found for %s in the last 7 days.", symbol), nil
	}
	if len(items) > 5 {
		items = items[:5]
	}
	var sb strings.Builder
	for i, item := range items {
		t := time.Unix(item.Datetime, 0).Format("2006-01-02")
		fmt.Fprintf(&sb, "%d. [%s] %s (%s)\n   %s\n   %s\n", i+1, t, item.Headline, item.Source, item.Summary, item.URL)
	}
	return sb.String(), nil
}

func executeFinnhubGetEarningsCalendar(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	from := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	to := time.Now().AddDate(0, 0, 90).Format("2006-01-02")
	logger.Info("finnhub get earnings calendar", "tool", "finnhub_get_earnings_calendar", "symbol", symbol, "from", from, "to", to)
	path := fmt.Sprintf("/calendar/earnings?symbol=%s&from=%s&to=%s", url.QueryEscape(symbol), from, to)
	raw, err := finnhubGET(ctx, path)
	if err != nil {
		logger.Error("finnhub earnings calendar failed", "tool", "finnhub_get_earnings_calendar", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub earnings calendar failed: %w", err)
	}
	var cal finnhubEarningsCalendar
	if err := json.Unmarshal(raw, &cal); err != nil {
		logger.Error("finnhub earnings calendar parse failed", "tool", "finnhub_get_earnings_calendar", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get earnings calendar completed", "tool", "finnhub_get_earnings_calendar", "symbol", symbol, "events", len(cal.EarningsCalendar))
	if len(cal.EarningsCalendar) == 0 {
		return fmt.Sprintf("No earnings events found for %s.", symbol), nil
	}
	var sb strings.Builder
	for _, e := range cal.EarningsCalendar {
		fmt.Fprintf(&sb, "%s Q%d %d (%s) | EPS est: %.2f act: %.2f | Rev est: $%.2fB act: $%.2fB\n",
			e.Symbol, e.Quarter, e.Year, e.Date,
			e.EPSEstimate, e.EPSActual,
			e.RevenueEstimate/1e9, e.RevenueActual/1e9,
		)
	}
	return sb.String(), nil
}

func executeFinnhubGetAnalystRecommendations(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	logger.Info("finnhub get analyst recommendations", "tool", "finnhub_get_analyst_recommendations", "symbol", symbol)
	raw, err := finnhubGET(ctx, "/stock/recommendation?symbol="+url.QueryEscape(symbol))
	if err != nil {
		logger.Error("finnhub recommendations failed", "tool", "finnhub_get_analyst_recommendations", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub recommendations failed: %w", err)
	}
	var recs []finnhubRecommendation
	if err := json.Unmarshal(raw, &recs); err != nil {
		logger.Error("finnhub recommendations parse failed", "tool", "finnhub_get_analyst_recommendations", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(recs) == 0 {
		return fmt.Sprintf("No analyst recommendations found for %s.", symbol), nil
	}
	r := recs[0]
	total := r.StrongBuy + r.Buy + r.Hold + r.Sell + r.StrongSell
	logger.Info("finnhub get analyst recommendations completed", "tool", "finnhub_get_analyst_recommendations", "symbol", symbol, "period", r.Period, "total_analysts", total)
	return fmt.Sprintf(
		"%s analyst consensus (%s) | Strong Buy: %d | Buy: %d | Hold: %d | Sell: %d | Strong Sell: %d | Total: %d",
		symbol, r.Period, r.StrongBuy, r.Buy, r.Hold, r.Sell, r.StrongSell, total,
	), nil
}

func executeFinnhubGetInsiderSentiment(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	from := time.Now().AddDate(0, -3, 0).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")
	logger.Info("finnhub get insider sentiment", "tool", "finnhub_get_insider_sentiment", "symbol", symbol, "from", from, "to", to)
	path := fmt.Sprintf("/stock/insider-sentiment?symbol=%s&from=%s&to=%s", url.QueryEscape(symbol), from, to)
	raw, err := finnhubGET(ctx, path)
	if err != nil {
		logger.Error("finnhub insider sentiment failed", "tool", "finnhub_get_insider_sentiment", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub insider sentiment failed: %w", err)
	}
	var sentiment finnhubInsiderSentiment
	if err := json.Unmarshal(raw, &sentiment); err != nil {
		logger.Error("finnhub insider sentiment parse failed", "tool", "finnhub_get_insider_sentiment", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get insider sentiment completed", "tool", "finnhub_get_insider_sentiment", "symbol", symbol, "records", len(sentiment.Data))
	if len(sentiment.Data) == 0 {
		return fmt.Sprintf("No insider sentiment data found for %s.", symbol), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s insider sentiment (last 3 months):\n", symbol)
	for _, d := range sentiment.Data {
		fmt.Fprintf(&sb, "  %d-%02d | Net share change: %d | MSPR: %.4f\n", d.Year, d.Month, d.Change, d.MSPR)
	}
	return sb.String(), nil
}

func executeFinnhubGetPriceTarget(ctx context.Context, args map[string]any) (string, error) {
	symbol, err := requireSymbol(args)
	if err != nil {
		return "", err
	}
	logger.Info("finnhub get price target", "tool", "finnhub_get_price_target", "symbol", symbol)
	raw, err := finnhubGET(ctx, "/stock/price-target?symbol="+url.QueryEscape(symbol))
	if err != nil {
		logger.Error("finnhub price target failed", "tool", "finnhub_get_price_target", "symbol", symbol, "error", err)
		return "", fmt.Errorf("finnhub price target failed: %w", err)
	}
	var pt finnhubPriceTarget
	if err := json.Unmarshal(raw, &pt); err != nil {
		logger.Error("finnhub price target parse failed", "tool", "finnhub_get_price_target", "symbol", symbol, "error", err)
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	logger.Info("finnhub get price target completed", "tool", "finnhub_get_price_target", "symbol", symbol, "mean", pt.TargetMean)
	return fmt.Sprintf(
		"%s price targets (as of %s) | Mean: $%.2f | Median: $%.2f | High: $%.2f | Low: $%.2f",
		symbol, pt.LastUpdated, pt.TargetMean, pt.TargetMedian, pt.TargetHigh, pt.TargetLow,
	), nil
}
