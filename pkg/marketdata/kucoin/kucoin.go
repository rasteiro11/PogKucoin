package kucoin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rasteiro11/PogCore/pkg/logger"
	"github.com/rasteiro11/PogKucoin/models"
	"github.com/rasteiro11/PogKucoin/pkg/marketdata"
	"github.com/shopspring/decimal"
)

var ErrAssetPidNotFound = errors.New("error asset pid not found")

type InvestingOption func(*investinProvider)

type investinProvider struct {
	websocket   *Websocket
	messageChan <-chan Message
	message     Message
}

type Message struct {
	PID         string  `json:"pid"`
	LastDir     string  `json:"last_dir"`
	LastNumeric float64 `json:"last_numeric"`
	Last        string  `json:"last"`
	Bid         string  `json:"bid"`
	Ask         string  `json:"ask"`
	High        string  `json:"high"`
	Low         string  `json:"low"`
	LastClose   string  `json:"last_close"`
	PC          string  `json:"pc"`
	PCP         string  `json:"pcp"`
	PCCol       string  `json:"pc_col"`
	Time        string  `json:"time"`
	Timestamp   int64   `json:"timestamp"`
}

type Payload struct {
	Message string `json:"message"`
}

type Websocket struct {
	url         *url.URL
	once        sync.Once
	conn        *websocket.Conn
	httpClient  *http.Client
	retry       chan struct{}
	done        chan struct{}
	ready       chan struct{}
	writer      chan []byte
	priceStream chan models.MarketData
}

type InstanceServer struct {
	Endpoint     string `json:"endpoint"`
	Encrypt      bool   `json:"encrypt"`
	Protocol     string `json:"protocol"`
	PingInterval int    `json:"pingInterval"`
	PingTimeout  int    `json:"pingTimeout"`
}

type InstanceServerData struct {
	Token           string           `json:"token"`
	InstanceServers []InstanceServer `json:"instanceServers"`
}

type ServerInstanceMessage struct {
	Code string             `json:"code"`
	Data InstanceServerData `json:"data"`
}

type WebsockeOption func(*Websocket)

func WithHttlClient(httpClient *http.Client) WebsockeOption {
	return func(w *Websocket) {
		w.httpClient = httpClient
	}
}

func NewMarketDataProvider(ctx context.Context, opts ...WebsockeOption) (marketdata.MarketDataProvider, error) {
	ws := &Websocket{
		done:        make(chan struct{}),
		priceStream: make(chan models.MarketData, 1),
	}

	for _, opt := range opts {
		opt(ws)
	}

	if ws.httpClient == nil {
		ws.httpClient = &http.Client{
			Timeout: time.Second * 30,
		}
	}

	res, err := NewRequest[ServerInstanceMessage](ctx, "https://api.kucoin.com/api/v1/bullet-public", http.MethodPost,
		WithHTTPClient(ws.httpClient),
	)
	if err != nil {
		return nil, err
	}

	logger.Of(ctx).Debugf("RES: %+v\n", res)

	url, err := url.Parse(res.Data.InstanceServers[0].Endpoint)
	if err != nil {
		return nil, err
	}

	q := url.Query()
	q.Set("token", res.Data.Token)
	url.RawQuery = q.Encode()

	ws.url = url

	logger.Of(ctx).Debugf("URL: %+v\n", ws.url.String())

	return ws, nil
}

func (w *Websocket) pingHandler(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	<-w.ready

	for {
		select {
		case <-w.done:
			return
		case <-w.retry:
			select {
			case <-ctx.Done():
				return
			case _, ok := <-w.done:
				if !ok {
					return
				}
			default:
			}
		case <-ticker.C:
			logger.Of(ctx).Debugf("Sending ping message")

			if err := w.conn.WriteMessage(websocket.PongMessage, nil); err != nil {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-w.done:
					if !ok {
						return
					}
				default:
				}

				logger.Of(ctx).Errorf("pongMessage error: %+v", err)

				w.retry <- struct{}{}
				return
			}
		}
	}
}

func (w *Websocket) sendMessage(ctx context.Context) {
	for {
		select {
		case <-w.done:
			return
		case message := <-w.writer:
			select {
			case <-ctx.Done():
				return
			case _, ok := <-w.done:
				if !ok {
					return
				}
			default:
			}

			logger.Of(ctx).Debugf("Sending message: %s", message)

			if err := w.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-w.done:
					if !ok {
						return
					}
				default:
				}

				logger.Of(ctx).Errorf("writeMessage error: %+v", err)

				w.retry <- struct{}{}

				return
			}
		}
	}
}

func (w *Websocket) Start(ctx context.Context, currency ...string) (<-chan models.MarketData, error) {
	w.priceStream = make(chan models.MarketData, 1)

	payload, err := prepareSubscriberPayload(currency...)
	if err != nil {
		return nil, err
	}

	logger.Of(ctx).Debugf("PAYLOAD: %+v\n", string(payload))

	go func() {
		defer func() {
			if w.conn != nil {
				logger.Of(ctx).Debug("closing websocket connection")

				if err := w.conn.Close(); err != nil {
					logger.Of(ctx).Warn("failed to close websocket connection")
				}
			}
		}()

	Retry:
		w.retry = make(chan struct{}, 1)
		w.ready = make(chan struct{}, 1)
		w.writer = make(chan []byte)

		// logger.Of(ctx).Debugf("Starting connection to: %s", w.url.String())

		c, _, err := websocket.DefaultDialer.Dial(w.url.String(), nil)
		if err != nil {
			goto Retry
		}

		logger.Of(ctx).Debug("Successfully connected")

		go w.sendMessage(ctx)
		go w.pingHandler(ctx)

		w.conn = c
		close(w.ready)

		go func() { w.writer <- []byte(payload) }()

		for {
			select {
			case <-w.done:
				return
			case <-w.retry:
				goto Retry
			default:
				select {
				case <-ctx.Done():
					return
				case _, ok := <-w.done:
					if !ok {
						return
					}
				default:
				}

				_, message, err := c.ReadMessage()
				if err != nil {
					logger.Of(ctx).Errorf("readMessage: %+v\n", err)
					goto Retry
				}

				ticker := &TickerEvent{}
				if err := json.Unmarshal(message, ticker); err != nil {
					continue
				}

				w.priceStream <- models.MarketData{
					Ask:            ticker.Data.Data.Sell,
					BaseVolume24h:  ticker.Data.Data.MarketChange24h.Vol,
					Bid:            ticker.Data.Data.Buy,
					High24h:        ticker.Data.Data.MarketChange24h.High,
					LastPrice:      ticker.Data.Data.LastTradedPrice,
					Low24h:         ticker.Data.Data.MarketChange24h.Low,
					Open24h:        ticker.Data.Data.MarketChange24h.Open,
					QuoteVolume24h: ticker.Data.Data.MarketChange24h.Vol,
					Symbol:         ticker.Data.Data.QuoteCurrency,
					Timestamp:      ticker.Data.Data.Datetime,
				}

				// output := message[3 : len(message)-2]
				// processedJson := backslashMatcher.ReplaceAll([]byte(output), []byte(""))
				// match := payloadMatcher.FindStringSubmatch(string(processedJson))

				// if len(match) >= 2 {
				// 	jsonPayload := match[1]

				// 	var message Message
				// 	if err := json.Unmarshal([]byte(jsonPayload), &message); err != nil {
				// 		continue
				// 	}

				// 	logger.Of(ctx).Debugf("PAYLOAD: %s\n", jsonPayload)

				// 	select {
				// 	case <-ctx.Done():
				// 		return
				// 	case _, ok := <-w.done:
				// 		if !ok {
				// 			return
				// 		}
				// 	default:
				// 	}

				// 	w.priceStream <- message
				// }
			}
		}
	}()

	return w.priceStream, nil
}

func (w *Websocket) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	logger.Of(ctx).Debug("Closing connection")

	w.once.Do(func() {
		close(w.done)
		close(w.retry)
		close(w.priceStream)
	})

	return nil
}

type subscribePayload struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Topic    string `json:"topic"`
	Response bool   `json:"response"`
}

func prepareSubscriberPayload(currency ...string) ([]byte, error) {
	payload := []string{}

	for _, c := range currency {
		if strings.ToUpper(c) == "USDT" {
			logger.Global().Debugf("USDT GAMER")
			payload = append(payload, "USDT-BRL")
			continue
		}
		payload = append(payload, fmt.Sprintf("%s-USDT", strings.ToUpper(c)))
	}

	return json.Marshal(subscribePayload{
		ID:       time.Now().Unix(),
		Type:     "subscribe",
		Topic:    "/market/snapshot:" + strings.Join(payload, ","),
		Response: false,
	})
}

type MarketChange struct {
	ChangePrice float64         `json:"changePrice"`
	ChangeRate  float64         `json:"changeRate"`
	High        decimal.Decimal `json:"high"`
	Low         decimal.Decimal `json:"low"`
	Open        decimal.Decimal `json:"open"`
	Vol         decimal.Decimal `json:"vol"`
	VolValue    float64         `json:"volValue"`
}

type Data struct {
	AskSize          float64         `json:"askSize"`
	AveragePrice     float64         `json:"averagePrice"`
	BaseCurrency     string          `json:"baseCurrency"`
	BidSize          float64         `json:"bidSize"`
	Board            int             `json:"board"`
	Buy              decimal.Decimal `json:"buy"`
	ChangePrice      float64         `json:"changePrice"`
	ChangeRate       float64         `json:"changeRate"`
	Close            decimal.Decimal `json:"close"`
	Datetime         int64           `json:"datetime"`
	High             decimal.Decimal `json:"high"`
	LastTradedPrice  decimal.Decimal `json:"lastTradedPrice"`
	Low              decimal.Decimal `json:"low"`
	MakerCoefficient float64         `json:"makerCoefficient"`
	MakerFeeRate     float64         `json:"makerFeeRate"`
	MarginTrade      bool            `json:"marginTrade"`
	Mark             int             `json:"mark"`
	Market           string          `json:"market"`
	MarketChange1h   MarketChange    `json:"marketChange1h"`
	MarketChange24h  MarketChange    `json:"marketChange24h"`
	MarketChange4h   MarketChange    `json:"marketChange4h"`
	Markets          []string        `json:"markets"`
	Open             decimal.Decimal `json:"open"`
	QuoteCurrency    string          `json:"quoteCurrency"`
	Sell             decimal.Decimal `json:"sell"`
	Sort             int             `json:"sort"`
	Symbol           string          `json:"symbol"`
	SymbolCode       string          `json:"symbolCode"`
	TakerCoefficient float64         `json:"takerCoefficient"`
	TakerFeeRate     float64         `json:"takerFeeRate"`
	Trading          bool            `json:"trading"`
	Vol              decimal.Decimal `json:"vol"`
	VolValue         float64         `json:"volValue"`
}

type TickerEvent struct {
	Type    string `json:"type"`
	Topic   string `json:"topic"`
	Subject string `json:"subject"`
	Data    struct {
		Sequence string `json:"sequence"`
		Data     Data   `json:"data"`
	} `json:"data"`
}
