package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

const AevoHttp string = "https://api.aevo.xyz"
const AevoWss string = "wss://ws.aevo.xyz"

type Greeks struct {
	Delta float64 `json:"delta,string"`
	Theta float64 `json:"theta,string"`
	Gamma float64 `json:"gamma,string"`
	Rho   float64 `json:"rho,string"`
	Vega  float64 `json:"vega,string"`
	Iv    float64 `json:"iv,string"`
}

type Market struct {
	InstrumentId     int     `json:"instrument_id,string"`
	InstrumentName   string  `json:"instrument_name"`
	InstrumentType   string  `json:"instrument_type"`
	UnderlyingAsset  string  `json:"underlying_asset"`
	QuoteAsset       string  `json:"quote_asset"`
	PriceStep        float64 `json:"price_step,string"`
	AmountStep       float64 `json:"amount_step,string"`
	MinOrderValue    float64 `json:"min_order_value,string"`
	MaxOrderValue    float64 `json:"max_order_value,string"`
	MaxNotionalValue float64 `json:"max_notional_value,string"`
	MarkPrice        float64 `json:"mark_price,string"`
	ForwardPrice     float64 `json:"forward_price,string"`
	IndexPrice       float64 `json:"index_price,string"`
	IsActive         bool    `json:"is_active"`
	OptionType       string  `json:"option_type"`
	Expiry           int     `json:"expiry,string"`
	Strike           int     `json:"strike,string"`
	Greeks           Greeks  `json:"greeks"`
}

var Orderbooks map[string]interface{} = make(map[string]interface{})

func markets(asset string) []Market {
	url := AevoHttp + "/markets?asset=" + asset + "&instrument_type=OPTION"

	req, _ := http.NewRequest("GET", url, nil) //NewRequest + Client.Do used to pass headers, otherwise http.Get can be used

	req.Header.Add("accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("markets request error: %v", err)
	}

	defer res.Body.Close() //Client.Do, http.Get, http.Post, etc all need response Body to be closed when done reading from it
	// defer defers execution until enclosing function returns

	var markets []Market

	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&markets)
	if err != nil {
		log.Fatalf("markets json decode error: %v", err)
	}

	return markets
}

func instruments(markets []Market) []string {
	var instruments []string
	for _, market := range markets {
		instruments = append(instruments, market.InstrumentName)
	}

	return instruments
}

func orderbookJson(instruments []string) []byte {
	var orderbooks []string
	for _, instrument := range instruments {
		orderbooks = append(orderbooks, "orderbook:"+instrument)
	}

	type wssData struct {
		Op   string   `json:"op"`
		Data []string `json:"data"`
	}

	data := wssData{
		Op:   "subscribe",
		Data: orderbooks,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("orderbook json marshal error: %v", err)
	}

	return jsonData
}

func wssReqOrderbook(instruments []string, ctx context.Context, c *websocket.Conn) {
	var data []byte
	for i := 0; true; i += 20 {
		if i+20 < len(instruments) {
			data = orderbookJson(instruments[i : i+20])
		} else {
			data = orderbookJson(instruments[i:])
		}

		fmt.Printf("%v\n\n", string(data))
		err := c.Write(ctx, 1, data)
		if err != nil {
			log.Fatalf("Write error: %v", err)
		}

		if i+20 > len(instruments) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// func unmarshalRes(raw []byte, res *map[string]interface{}) {
// 	err := json.Unmarshal(raw, &res)
// 	if err != nil {
// 		log.Fatalf("Error decoding: %s", err)
// 	}
// }

func wssRead(ctx context.Context, c *websocket.Conn) []byte {
	_, raw, err := c.Read(ctx)
	if err != nil {
		log.Fatalf("Read error: %v", err)
	}

	return raw
}

func constructOrderbooks(instruments []string) {
	for _, instrument := range instruments {
		Orderbooks[instrument] = make(map[string]interface{})
	}
}

// func updateOrderbooks(res map[string]interface{}) {
// 	key := res["channel"]["orderbook"]
// 	Orderbooks[key] = res
// }

func main() {
	markets := markets("ETH")
	instruments := instruments(markets)
	fmt.Printf("Number of instruments: %v\n\n", len(instruments))
	constructOrderbooks(instruments)
	// fmt.Printf("%+v", Orderbooks)
	// fmt.Printf("%v\n", instruments)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*20)
	defer cancel()

	c, res, err := websocket.Dial(ctx, AevoWss, nil)
	if err != nil {
		log.Fatalf("Dial error: %v", err)
	}
	fmt.Printf("%v\n\n", res)
	defer c.Close(websocket.StatusNormalClosure, "")
	defer c.CloseNow()

	wssReqOrderbook(instruments, ctx, c)

	var orderbookRaw []byte
	// var orderbookRes map[string]interface{}
	for {
		orderbookRaw = wssRead(ctx, c)
		// go unmarshalRes(orderbookRaw, &orderbookRes)
		go fmt.Printf("Decoded JSON response:\n %+v\n", string(orderbookRaw))
		// go fmt.Printf("%+v\n", reflect.TypeOf(orderbookRes["channel"]["orderbook"]))
	}
}

// _, r, err := c.Reader(ctx)
// if err != nil {
// 	log.Fatal(err)
// }
// fmt.Printf("%+v\n\n", r)

// var orderbookRes map[string]interface{}
// decoder := json.NewDecoder(r)
// err = decoder.Decode(&orderbookRes)
// if err != nil {
// 	log.Fatalf("Error decoding: %s", err)
// }
// fmt.Printf("Decoded JSON response:\n %+v\n", orderbookRes)
