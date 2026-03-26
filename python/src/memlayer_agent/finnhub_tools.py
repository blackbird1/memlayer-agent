# Example tool integration: Finnhub stock market data.
# This module shows how to add local (non-MCP) tools to the agent.
# Copy this pattern to integrate any REST API as a local tool.
# Loaded automatically when FINNHUB_API_KEY is set in the environment.

import os
from datetime import date, timedelta
from typing import Any, Callable
from urllib.parse import quote

import httpx
import structlog
from google.genai import types as genai_types

logger = structlog.get_logger(__name__)

FINNHUB_BASE_URL = "https://finnhub.io/api/v1"

ToolExecutor = Callable[[dict[str, Any]], str]


def _finnhub_api_key() -> str:
    key = os.environ.get("FINNHUB_API_KEY", "")
    if not key:
        raise ValueError("FINNHUB_API_KEY is not set")
    return key


async def _finnhub_get(path: str) -> Any:
    key = _finnhub_api_key()
    sep = "&" if "?" in path else "?"
    url = f"{FINNHUB_BASE_URL}{path}{sep}token={quote(key)}"
    async with httpx.AsyncClient() as client:
        resp = await client.get(url, timeout=10.0)
        resp.raise_for_status()
        return resp.json()


def _require_symbol(args: dict[str, Any]) -> str:
    symbol = args.get("symbol", "")
    if not symbol:
        raise ValueError("symbol is required")
    return symbol


# --- Executors ---

async def _search_tickers(args: dict[str, Any]) -> str:
    import json
    query = args.get("query", "")
    if not query:
        raise ValueError("query is required")
    logger.info("finnhub search tickers", tool="finnhub_search_tickers", query=query)
    data = await _finnhub_get(f"/search?q={quote(query)}")
    results = data.get("result", [])
    logger.info("finnhub search tickers completed", tool="finnhub_search_tickers", query=query, count=len(results))
    return json.dumps({"count": data.get("count", 0), "result": results})


async def _get_quote(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    logger.info("finnhub get quote", tool="finnhub_get_quote", symbol=symbol)
    q = await _finnhub_get(f"/quote?symbol={quote(symbol)}")
    logger.info("finnhub get quote completed", tool="finnhub_get_quote", symbol=symbol, price=q.get("c"))
    return (
        f"{symbol}: ${q.get('c', 0):.2f} "
        f"(change: {q.get('d', 0):.2f}, {q.get('dp', 0):.2f}%) | "
        f"Open: ${q.get('o', 0):.2f} | High: ${q.get('h', 0):.2f} | "
        f"Low: ${q.get('l', 0):.2f} | Prev Close: ${q.get('pc', 0):.2f}"
    )


async def _get_company_profile(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    logger.info("finnhub get company profile", tool="finnhub_get_company_profile", symbol=symbol)
    p = await _finnhub_get(f"/stock/profile2?symbol={quote(symbol)}")
    logger.info("finnhub get company profile completed", tool="finnhub_get_company_profile", symbol=symbol, company=p.get("name"))
    market_cap = p.get("marketCapitalization", 0)
    shares = p.get("shareOutstanding", 0)
    return (
        f"{p.get('name')} ({p.get('ticker')}) | Exchange: {p.get('exchange')} | "
        f"Industry: {p.get('finnhubIndustry')} | IPO: {p.get('ipo')} | "
        f"Market Cap: ${market_cap / 1000:.2f}B | Shares Out: {shares:.2f}M | "
        f"Currency: {p.get('currency')} | Country: {p.get('country')} | Web: {p.get('weburl')}"
    )


async def _get_company_news(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    today = date.today()
    from_date = (today - timedelta(days=7)).isoformat()
    to_date = today.isoformat()
    logger.info("finnhub get company news", tool="finnhub_get_company_news", symbol=symbol, from_date=from_date, to_date=to_date)
    items = await _finnhub_get(f"/company-news?symbol={quote(symbol)}&from={from_date}&to={to_date}")
    logger.info("finnhub get company news completed", tool="finnhub_get_company_news", symbol=symbol, articles=len(items))
    if not items:
        return f"No news found for {symbol} in the last 7 days."
    items = items[:5]
    lines = []
    for i, item in enumerate(items, 1):
        from datetime import datetime
        dt = datetime.fromtimestamp(item.get("datetime", 0)).strftime("%Y-%m-%d")
        lines.append(
            f"{i}. [{dt}] {item.get('headline')} ({item.get('source')})\n"
            f"   {item.get('summary')}\n"
            f"   {item.get('url')}"
        )
    return "\n".join(lines)


async def _get_earnings_calendar(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    today = date.today()
    from_date = (today - timedelta(days=30)).isoformat()
    to_date = (today + timedelta(days=90)).isoformat()
    logger.info("finnhub get earnings calendar", tool="finnhub_get_earnings_calendar", symbol=symbol, from_date=from_date, to_date=to_date)
    data = await _finnhub_get(f"/calendar/earnings?symbol={quote(symbol)}&from={from_date}&to={to_date}")
    cal = data.get("earningsCalendar", [])
    logger.info("finnhub get earnings calendar completed", tool="finnhub_get_earnings_calendar", symbol=symbol, events=len(cal))
    if not cal:
        return f"No earnings events found for {symbol}."
    lines = []
    for e in cal:
        rev_est = e.get("revenueEstimate", 0) or 0
        rev_act = e.get("revenueActual", 0) or 0
        lines.append(
            f"{e.get('symbol')} Q{e.get('quarter')} {e.get('year')} ({e.get('date')}) | "
            f"EPS est: {e.get('epsEstimate', 0):.2f} act: {e.get('epsActual', 0):.2f} | "
            f"Rev est: ${rev_est / 1e9:.2f}B act: ${rev_act / 1e9:.2f}B"
        )
    return "\n".join(lines)


async def _get_analyst_recommendations(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    logger.info("finnhub get analyst recommendations", tool="finnhub_get_analyst_recommendations", symbol=symbol)
    recs = await _finnhub_get(f"/stock/recommendation?symbol={quote(symbol)}")
    if not recs:
        return f"No analyst recommendations found for {symbol}."
    r = recs[0]
    total = r.get("strongBuy", 0) + r.get("buy", 0) + r.get("hold", 0) + r.get("sell", 0) + r.get("strongSell", 0)
    logger.info("finnhub get analyst recommendations completed", tool="finnhub_get_analyst_recommendations", symbol=symbol, period=r.get("period"), total=total)
    return (
        f"{symbol} analyst consensus ({r.get('period')}) | "
        f"Strong Buy: {r.get('strongBuy', 0)} | Buy: {r.get('buy', 0)} | "
        f"Hold: {r.get('hold', 0)} | Sell: {r.get('sell', 0)} | "
        f"Strong Sell: {r.get('strongSell', 0)} | Total: {total}"
    )


async def _get_insider_sentiment(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    today = date.today()
    from_date = (today - timedelta(days=90)).isoformat()
    to_date = today.isoformat()
    logger.info("finnhub get insider sentiment", tool="finnhub_get_insider_sentiment", symbol=symbol, from_date=from_date, to_date=to_date)
    data = await _finnhub_get(f"/stock/insider-sentiment?symbol={quote(symbol)}&from={from_date}&to={to_date}")
    records = data.get("data", [])
    logger.info("finnhub get insider sentiment completed", tool="finnhub_get_insider_sentiment", symbol=symbol, records=len(records))
    if not records:
        return f"No insider sentiment data found for {symbol}."
    lines = [f"{symbol} insider sentiment (last 3 months):"]
    for d in records:
        lines.append(f"  {d.get('year')}-{d.get('month'):02d} | Net share change: {d.get('change')} | MSPR: {d.get('mspr', 0):.4f}")
    return "\n".join(lines)


async def _get_price_target(args: dict[str, Any]) -> str:
    symbol = _require_symbol(args)
    logger.info("finnhub get price target", tool="finnhub_get_price_target", symbol=symbol)
    pt = await _finnhub_get(f"/stock/price-target?symbol={quote(symbol)}")
    logger.info("finnhub get price target completed", tool="finnhub_get_price_target", symbol=symbol, mean=pt.get("targetMean"))
    return (
        f"{symbol} price targets (as of {pt.get('lastUpdated')}) | "
        f"Mean: ${pt.get('targetMean', 0):.2f} | Median: ${pt.get('targetMedian', 0):.2f} | "
        f"High: ${pt.get('targetHigh', 0):.2f} | Low: ${pt.get('targetLow', 0):.2f}"
    )


# --- Registry ---

_SYMBOL_PARAM = {
    "symbol": genai_types.Schema(
        type=genai_types.Type.STRING,
        description="Ticker symbol (e.g. AAPL, MSFT).",
    )
}

TOOL_DECLARATIONS = [
    genai_types.FunctionDeclaration(
        name="finnhub_search_tickers",
        description="Search for stock tickers by company name or symbol.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties={
                "query": genai_types.Schema(
                    type=genai_types.Type.STRING,
                    description="Company name or ticker symbol to search for.",
                )
            },
            required=["query"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_quote",
        description="Fetch the current stock price and intraday quote data.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_company_profile",
        description="Fetch company profile: name, exchange, industry, market cap, IPO date, website.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_company_news",
        description="Fetch the latest company news headlines (last 7 days, up to 5 articles).",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_earnings_calendar",
        description="Fetch upcoming or recent earnings dates and EPS/revenue estimates for a symbol (next 90 days).",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_analyst_recommendations",
        description="Fetch the latest analyst buy/hold/sell recommendation consensus.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_insider_sentiment",
        description="Fetch insider trading sentiment (MSPR score and net share change) for the last 3 months.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
    genai_types.FunctionDeclaration(
        name="finnhub_get_price_target",
        description="Fetch analyst consensus price target: high, low, mean, and median.",
        parameters=genai_types.Schema(
            type=genai_types.Type.OBJECT,
            properties=_SYMBOL_PARAM,
            required=["symbol"],
        ),
    ),
]

TOOL_EXECUTORS: dict[str, ToolExecutor] = {
    "finnhub_search_tickers": _search_tickers,
    "finnhub_get_quote": _get_quote,
    "finnhub_get_company_profile": _get_company_profile,
    "finnhub_get_company_news": _get_company_news,
    "finnhub_get_earnings_calendar": _get_earnings_calendar,
    "finnhub_get_analyst_recommendations": _get_analyst_recommendations,
    "finnhub_get_insider_sentiment": _get_insider_sentiment,
    "finnhub_get_price_target": _get_price_target,
}
