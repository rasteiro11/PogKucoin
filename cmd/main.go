package main

import (
	"context"
	kucoinProvider "github.com/rasteiro11/PogKucoin/pkg/marketdata/kucoin"

	"github.com/rasteiro11/PogCore/pkg/logger"
)

func main() {
	ctx := context.Background()

	kucoinWs, err := kucoinProvider.NewMarketDataProvider(ctx)
	if err != nil {
		logger.Of(ctx).Fatalf("[main] kucoinWs.Start() returned error: %+v\n", err)
	}

	c, err := kucoinWs.Start(ctx, "usdt")
	if err != nil {
		logger.Of(ctx).Fatalf("[main] kucoinWs.Start() returned error: %+v\n", err)
	}

	for md := range c {
		logger.Of(ctx).Debugf("MARKET DATA: %+v\n", md)
	}

	for {
	}
}
