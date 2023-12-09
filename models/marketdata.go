package models

import "github.com/shopspring/decimal"

type MarketData struct {
	Ask            decimal.Decimal `json:"ask"`
	BaseVolume24h  decimal.Decimal `json:"baseVolume24h"`
	Bid            decimal.Decimal `json:"bid"`
	High24h        decimal.Decimal `json:"high24h"`
	LastPrice      decimal.Decimal `json:"lastPrice"`
	Low24h         decimal.Decimal `json:"low24h"`
	Open24h        decimal.Decimal `json:"open24h"`
	QuoteVolume24h decimal.Decimal `json:"quoteVolume24h"`
	Symbol         string          `json:"symbol"`
	Timestamp      int64           `json:"timestamp"`
}
