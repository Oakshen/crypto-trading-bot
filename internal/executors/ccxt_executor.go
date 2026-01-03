package executors

import (
	"fmt"

	"github.com/ccxt/ccxt/go/v4"
)

func main() {
	exchange := ccxt.NewBinance(map[string]interface{}{
		"apiKey": "MY KEY",
		"secret": "MY SECRET",
	})
	orderParams := map[string]interface{}{
		"clientOrderId": "myOrderId68768678",
	}

	exchange.LoadMarkets()

	order, err := exchange.CreateOrder("BTC/USDT", "limit", "buy", 0.001, ccxt.WithCreateOrderPrice(6000), ccxt.WithCreateOrderParams(orderParams))
	if err != nil {
		if ccxtError, ok := err.(*ccxt.Error); ok {
			if ccxtError.Type == "InvalidOrder" {
				fmt.Println("Invalid order")
			} else {
				fmt.Println("Some other error")
			}
		}
	} else {
		fmt.Println(*order.Id)
	}

	//// fetching OHLCV
	//ohlcv, err := exchange.FetchOHLCV("BTC/USDT", ccxt.WithFetchOHLCVTimeframe("5m"), ccxt.WithFetchOHLCVLimit(100))
	//
	//if err != nil {
	//	fmt.Println("Error: ", err)\
	//
	//
	//} else {
	//	fmt.Println("Got OHLCV!")
	//}
}
