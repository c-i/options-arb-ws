package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
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

type WssOrderbookData struct {
	Type           string      `json:"type"`
	InstrumentId   int         `json:"instrument_id,string"`
	InstrumentName string      `json:"instrument_name"`
	InstrumentType string      `json:"instrument_type"`
	Bids           [][]float64 `json:"bids"` //probably wont work
	Asks           [][]float64 `json:"asks"`
	LastUpdated    int         `json:"last_updated,string"`
	Checksum       int         `json:"checksum,string"`
}

type WssOrderbook struct {
	Channel string `json:"channel"`
	Data    string `json:"data"`
}

func markets() []Market {
	url := AevoHttp + "/markets"

	req, _ := http.NewRequest("GET", url, nil) //NewRequest + Client.Do used to pass headers, otherwise http.Get can be used

	req.Header.Add("accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	defer res.Body.Close() //Client.Do, http.Get, http.Post, etc all need response Body to be closed when done reading from it
	// defer defers execution until enclosing function returns

	var markets []Market

	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&markets)
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}

	return jsonData
}

func main() {
	markets := markets()
	instruments := instruments(markets)
	// fmt.Printf("%v\n", instruments)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	c, res, err := websocket.Dial(ctx, AevoWss, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v\n", res)
	defer c.CloseNow()

	data := orderbookJson(instruments[1:5])
	err = wsjson.Write(ctx, c, data)
	if err != nil {
		log.Fatal(err)
	}

	_, raw, err := c.Read(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v\n", string(raw))

	var orderbookRes WssOrderbook
	err = json.Unmarshal(raw, &orderbookRes)
	if err != nil {
		log.Fatalf("Error unmarshaling: %s", err)
	}
	fmt.Printf("%+v\n", orderbookRes)

	c.Close(websocket.StatusNormalClosure, "")
}
