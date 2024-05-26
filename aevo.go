package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

func aevoMarkets(asset string) []Market {
	url := AevoHttp + "/markets?asset=" + asset + "&instrument_type=OPTION"

	req, _ := http.NewRequest("GET", url, nil) //NewRequest + Client.Do used to pass headers, otherwise http.Get can be used

	req.Header.Add("accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("aevoMarkets request error: %v", err)
	}

	defer res.Body.Close() //Client.Do, http.Get, http.Post, etc all need response Body to be closed when done reading from it
	// defer defers execution until enclosing function returns

	var markets []Market

	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&markets)
	if err != nil {
		log.Fatalf("aevoMarkets json decode error: %v", err)
	}

	return markets
}

func aevoInstruments(markets []Market) []string {
	var instruments []string
	for _, market := range markets {
		if market.IsActive {
			instruments = append(instruments, market.InstrumentName)
		}
	}

	return instruments
}

func aevoOrderbookJson(instruments []string) []byte {
	var orderbooks []string
	for _, instrument := range instruments {
		orderbooks = append(orderbooks, "orderbook:"+instrument)
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

func aevoIndexJson(assets []string) []byte {
	var indices []string
	for _, asset := range assets {
		indices = append(indices, "index:"+asset)
	}

	data := wssData{
		Op:   "subscribe",
		Data: indices,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("orderbook json marshal error: %v", err)
	}

	return jsonData
}

func aevoWssReqOrderbook(instruments []string, ctx context.Context, c *websocket.Conn) {
	var data []byte
	for i := 0; true; i += 20 {
		if i+20 < len(instruments) {
			data = aevoOrderbookJson(instruments[i : i+20])
		} else {
			data = aevoOrderbookJson(instruments[i:])
		}

		// fmt.Printf("subscribe: %v\n\n", string(data))
		err := c.Write(ctx, 1, data)
		if err != nil {
			log.Fatalf("Write error: %v\n", err)
		}

		if i+20 > len(instruments) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func aevoWssReqIndex(assets []string, ctx context.Context, c *websocket.Conn) {
	data := aevoIndexJson(assets)
	fmt.Printf("subscribe: %v\n\n", string(data))

	err := c.Write(ctx, 1, data)
	if err != nil {
		log.Fatalf("Write error: %v\n", err)
	}
}

// loop through []Orders and replace each element with best bid (highest) and best ask (lowest)

func aevoUpdateOrderbooks(res map[string]interface{}) {
	data, ok := res["data"].(map[string]interface{})
	if !ok {
		log.Printf("aevoUpdateOrderbooks: unable to cast response to type map[string]interface{}\n")
		return
	}

	// if len(data) <= 3 { //check for ping response, not very robust and inappropriate to catch here, might need to fix later
	// 	return
	// }

	instrument, ok := data["instrument_name"].(string)
	bidsRaw, bidsOk := data["bids"].([]interface{})
	asksRaw, asksOk := data["asks"].([]interface{})
	timeStr, timeOk := data["last_updated"].(string)
	if (!ok || !timeOk) || !(bidsOk || asksOk) {
		log.Printf("aevoUpdateOrderbooks: unable to convert field: response: %+v", res)
		return
	}

	if len(bidsRaw) <= 0 && len(asksRaw) <= 0 {
		return
	}

	bids, bidsErr := unpackOrders(bidsRaw, "aevo")
	asks, asksErr := unpackOrders(asksRaw, "aevo")
	if bidsErr != nil && asksErr != nil {
		log.Printf("unpackOrders error: \n%v\n", bidsErr)
		log.Printf("%v\n", asksErr)
		return
	}

	lastUpdated, err := strconv.ParseFloat(timeStr, 64)
	if err != nil {
		log.Printf("Failed to convert last_updated timestamp to int64: %v\n", err)
		return
	}

	_, exists := Orderbooks[instrument]

	if exists {

		Orderbooks[instrument].Bids = append(Orderbooks[instrument].Bids, bids...)
		Orderbooks[instrument].Asks = append(Orderbooks[instrument].Asks, asks...)
		Orderbooks[instrument].LastUpdated = lastUpdated
	} else {
		Orderbooks[instrument] = &OrderbookData{
			Bids:        bids,
			Asks:        asks,
			LastUpdated: lastUpdated,
		}
	}

	sort.Slice(Orderbooks[instrument].Bids, func(i, j int) bool {
		return Orderbooks[instrument].Bids[i].Price < Orderbooks[instrument].Bids[j].Price
	})
	sort.Slice(Orderbooks[instrument].Asks, func(i, j int) bool {
		return Orderbooks[instrument].Asks[i].Price < Orderbooks[instrument].Asks[j].Price
	})

	// fmt.Printf("%v: %+v\n\n", instrument, Orderbooks[instrument])
	// if strings.Contains(instrument, "-C") {
	// 	instrumentTrim, _ := strings.CutSuffix(instrument, "-C")
	// 	fmt.Printf("%v: %+v\n\n", instrumentTrim, ArbTables[instrumentTrim])
	// }
}

func aevoUpdateIndex(res map[string]interface{}) {
	AevoIndex.Mu.Lock()
	defer AevoIndex.Mu.Unlock()

	channel, ok := res["channel"].(string)
	if !ok {
		log.Printf("aevoUpdateIndex: unable to convert response 'channel' to string: %v\n\n", reflect.TypeOf(res["channel"]))
		return
	}

	data, ok := res["data"].(map[string]interface{})
	if !ok {
		log.Printf("aevoUpdateIndex: unable to cast response to type map[string]interface{}\n\n")
		return
	}
	// if reflect.TypeOf(data["price"]) == nil { //catch ping response, inappropriate to catch here, should fix later
	// 	return
	// }

	asset := strings.TrimPrefix(channel, "index:")
	// fmt.Printf("asset: %v\n\n", asset)

	priceStr, ok := data["price"].(string)
	if !ok {
		log.Printf("aevoUpdateIndex: unable to cast field to type string: %v\n\n", reflect.TypeOf(data["price"]))
		return
	}

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("aevoUpdateIndex: error converting string to float64: %v\n\n", err)
		return
	}

	AevoIndex.Index[asset] = price
	// fmt.Printf("index: %+v\n\n", Index)
}

func aevoWssRead(ctx context.Context, c *websocket.Conn) { //add exit condition, add ping or use Reader instead of Read to automatically manage ping, disconnect, etc
	var res map[string]interface{}
	raw, err := wssRead(ctx, c)
	if err != nil {
		log.Printf("aevoWssRead: %v\n(response): %v\n\n", err, string(raw))
		return
	}

	err = json.Unmarshal(raw, &res)
	if err != nil {
		log.Printf("aevoWssRead: error unmarshaling orderbookRaw: %v\n\n", err)
		return
	}

	channel, ok := res["channel"].(string)
	if !ok {
		log.Printf("aevoWssRead: unable to convert response 'channel' to string\n\n")
		return
	}

	if strings.Contains(channel, "orderbook") {
		aevoUpdateOrderbooks(res)
	}

	if strings.Contains(channel, "index") {
		aevoUpdateIndex(res)
		updateArbTables("ETH")
	}
}

func aevoWssReqLoop(ctx context.Context, c *websocket.Conn) {
	for {
		assets := []string{"ETH"}
		markets := aevoMarkets("ETH")
		instruments := aevoInstruments(markets)
		fmt.Printf("Aevo number of instruments: %v\n\n", len(instruments))

		aevoWssReqOrderbook(instruments, ctx, c)
		log.Printf("Requested Aevo Orderbooks")
		aevoWssReqIndex(assets, ctx, c)
		log.Printf("Requested Aevo Index")

		time.Sleep(time.Minute * 10)
	}
}
