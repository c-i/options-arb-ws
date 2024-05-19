package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"nhooyr.io/websocket"
)

const AevoHttp string = "https://api.aevo.xyz"
const AevoWss string = "wss://ws.aevo.xyz"
const LyraHttp string = "https://api.lyra.finance"
const LyraWss string = "wss://api.lyra.finance/ws"

type wssData struct {
	Op   string   `json:"op"`
	Data []string `json:"data"`
}

type Greeks struct {
	Delta float64 `json:"delta,string"`
	Theta float64 `json:"theta,string"`
	Gamma float64 `json:"gamma,string"`
	Rho   float64 `json:"rho,string"`
	Vega  float64 `json:"vega,string"`
	Iv    float64 `json:"iv,string"`
}

type Market struct {
	InstrumentId     int64   `json:"instrument_id,string"`
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
	Expiry           int64   `json:"expiry,string"`
	Strike           int64   `json:"strike,string"`
	Greeks           Greeks  `json:"greeks"`
}

type Order struct {
	Price  float64
	Amount float64
	Iv     float64
}

type OrderbookData struct {
	Bids        []Order
	Asks        []Order
	LastUpdated float64
}

type ArbTable struct {
	Asset     string
	Expiry    string
	Strike    float64
	Bids      []Order
	Asks      []Order
	BidType   string
	AskType   string
	AbsProfit float64
	RelProfit float64
	Apy       float64
}

var Orderbooks map[string]*OrderbookData = make(map[string]*OrderbookData) //pointer seems like a bad idea but makes assignment of elements easier
var ArbTables map[string]*ArbTable = make(map[string]*ArbTable)
var AevoIndex map[string]float64 = make(map[string]float64)
var LyraIndex map[string]float64 = make(map[string]float64)

func lyraMarkets(asset string) map[string]interface{} {
	url := LyraHttp + "/public/get_instruments"

	payload := strings.NewReader(fmt.Sprintf("{\"expired\":false,\"instrument_type\":\"option\",\"currency\":\"%v\"}", asset))

	req, _ := http.NewRequest("POST", url, payload)

	req.Header.Add("accept", "application/json")
	req.Header.Add("content-type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("lyraMarkets: request error: %v", err)
	}

	defer res.Body.Close()

	var markets map[string]interface{}

	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&markets)
	if err != nil {
		log.Fatalf("markets json decode error: %v", err)
	}

	return markets
}

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

func lyraInstruments(markets map[string]interface{}) []string {
	var instruments []string
	result, ok := markets["result"].([]interface{})
	if !ok {
		log.Printf("lyraInstruments: unable to convert markets['result'] to []interface{}")
		return instruments
	}

	var instrument string
	var market map[string]interface{}
	for _, item := range result {
		market = item.(map[string]interface{})
		instrument = market["instrument_name"].(string)

		instruments = append(instruments, instrument)
	}

	return instruments
}

func instruments(markets []Market) []string {
	var instruments []string
	for _, market := range markets {
		if market.IsActive {
			instruments = append(instruments, market.InstrumentName)
		}
	}

	return instruments
}

func lyraOrderbookJson(instruments []string) []byte {
	params := make(map[string][]string)
	params["channels"] = []string{}

	var param string
	for _, instrument := range instruments {
		param = "orderbook." + instrument + ".10.10"
		params["channels"] = append(params["channels"], param)
	}

	data := struct {
		Id     string              `json:"id"`
		Method string              `json:"method"`
		Params map[string][]string `json:"params"`
	}{
		"2",
		"subscribe",
		params,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("orderbook json marshal error: %v", err)
	}

	return jsonData
}

func orderbookJson(instruments []string) []byte {
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

func lyraIndexJson(assets []string) []byte {
	params := make(map[string][]string)
	params["channels"] = []string{}

	var param string
	for _, asset := range assets {
		param = "spot_feed." + asset
		params["channels"] = append(params["channels"], param)
	}

	data := struct {
		Id     string              `json:"id"`
		Method string              `json:"method"`
		Params map[string][]string `json:"params"`
	}{
		"2",
		"subscribe",
		params,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("orderbook json marshal error: %v", err)
	}

	return jsonData
}

func indexJson(assets []string) []byte {
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

func lyraWssReqOrderbook(instruments []string, ctx context.Context, c *websocket.Conn) {
	data := lyraOrderbookJson(instruments)

	err := c.Write(ctx, 1, data)
	if err != nil {
		log.Fatalf("Write error: %v\n", err)
	}
}

func wssReqOrderbook(instruments []string, ctx context.Context, c *websocket.Conn) {
	var data []byte
	for i := 0; true; i += 20 {
		if i+20 < len(instruments) {
			data = orderbookJson(instruments[i : i+20])
		} else {
			data = orderbookJson(instruments[i:])
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

func lyraWssReqIndex(assets []string, ctx context.Context, c *websocket.Conn) {
	data := lyraIndexJson(assets)
	fmt.Printf("subscribe: %v\n\n", string(data))

	err := c.Write(ctx, 1, data)
	if err != nil {
		log.Fatalf("Write error: %v\n", err)
	}
}

func wssReqIndex(assets []string, ctx context.Context, c *websocket.Conn) {
	data := indexJson(assets)
	fmt.Printf("subscribe: %v\n\n", string(data))

	err := c.Write(ctx, 1, data)
	if err != nil {
		log.Fatalf("Write error: %v\n", err)
	}
}

func unpackOrders(orders []interface{}, exchange string) ([]Order, error) {
	unpackedOrders := make([]Order, 0)
	for _, order := range orders {
		orderArr, ok := order.([]interface{})

		if !ok {
			return unpackedOrders, errors.New("orders not of []interface{} type")
		}
		if exchange == "aevo" && len(orderArr) != 3 {
			return unpackedOrders, errors.New("aevo orders not length 3")
		}
		if exchange == "lyra" && len(orderArr) != 2 {
			return unpackedOrders, errors.New("lyra orders not length 2")
		}

		priceStr, priceOk := orderArr[0].(string)
		amountStr, amountOk := orderArr[1].(string)
		var ivStr string
		var ivOk bool
		if exchange == "aevo" {
			ivStr, ivOk = orderArr[2].(string)
		}
		if exchange == "lyra" {
			ivStr = "-1"
			ivOk = true
		}
		if !priceOk || !amountOk || !ivOk {
			return unpackedOrders, errors.New("unable to convert interface{} element to string")
		}

		price, priceErr := strconv.ParseFloat(priceStr, 64)
		amount, amountErr := strconv.ParseFloat(amountStr, 64)
		iv, ivErr := strconv.ParseFloat(ivStr, 64)
		if priceErr != nil || amountErr != nil || ivErr != nil {
			log.Printf("%v\n", priceErr)
			log.Printf("%v\n", amountErr)
			log.Printf("%v\n", ivErr)
			return unpackedOrders, errors.New("error converting string to float64")
		}

		unpackedOrders = append(unpackedOrders, Order{price, amount, iv})
	}

	return unpackedOrders, nil
}

func lyraUpdateOrderbooks(data map[string]interface{}) {
	lyraInstrument, ok := data["instrument_name"].(string)
	bidsRaw, bidsOk := data["bids"].([]interface{})
	asksRaw, asksOk := data["asks"].([]interface{})
	timestamp, timeOk := data["timestamp"].(float64)
	if (!ok || !timeOk) || !(bidsOk || asksOk) {
		log.Printf("lyraUpdateOrderbooks: unable to convert field: response: %+v", data)
		return
	}

	if len(bidsRaw) <= 0 && len(asksRaw) <= 0 {
		return
	}

	bids, bidsErr := unpackOrders(bidsRaw, "lyra")
	asks, asksErr := unpackOrders(asksRaw, "lyra")
	if bidsErr != nil && asksErr != nil {
		log.Printf("unpackOrders error: \n%v\n", bidsErr)
		log.Printf("%v\n", asksErr)
		return
	}

	instrumentParts := strings.Split(lyraInstrument, "-")
	expiryTs, err := time.Parse("20060102", instrumentParts[1])
	if err != nil {
		log.Printf("lyraUpdateOrderbooks: time.Parse error: %v\n\n", err)
	}
	expiry := strings.ToUpper(expiryTs.Format("02Jan06"))
	instrument := instrumentParts[0] + "-" + expiry + "-" + instrumentParts[2] + "-" + instrumentParts[3]

	Orderbooks[instrument] = &OrderbookData{
		Bids:        bids,
		Asks:        asks,
		LastUpdated: timestamp,
	}

	// fmt.Printf("%v: %+v\n\n", instrument, Orderbooks[instrument])
}

func updateOrderbooks(res map[string]interface{}) {
	data, ok := res["data"].(map[string]interface{})
	if !ok {
		log.Printf("updateOrderbooks: unable to cast response to type map[string]interface{}\n")
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
		log.Printf("updateOrderbooks: unable to convert field: response: %+v", res)
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

	Orderbooks[instrument] = &OrderbookData{
		Bids:        bids,
		Asks:        asks,
		LastUpdated: lastUpdated,
	}

	fmt.Printf("%v: %+v\n\n", instrument, Orderbooks[instrument])
	// if strings.Contains(instrument, "-C") {
	// 	instrumentTrim, _ := strings.CutSuffix(instrument, "-C")
	// 	fmt.Printf("%v: %+v\n\n", instrumentTrim, ArbTables[instrumentTrim])
	// }
}

func lyraUpdateIndex(data map[string]interface{}) {
	feeds, ok := data["feeds"].(map[string]interface{})
	if !ok {
		log.Printf("lyraUpdateIndex: unable to convert data['feeds'] to map[string]interface{}: %+v\n\n", data)
		return
	}

	var feed map[string]interface{}
	for key, value := range feeds {
		feed, ok = value.(map[string]interface{})
		price, ok2 := feed["price"].(string)
		if !ok || !ok2 {
			log.Printf("lyraUpdateIndex: unable to convert value to map[string]interface{} or feed['price'] to float64:\n value, type: %+v, %+v\n feed['price']: %+v, %+v\n\n", value, reflect.TypeOf(value), feed["price"], reflect.TypeOf(feed["price"]))
			continue
		}

		var err error
		LyraIndex[key], err = strconv.ParseFloat(price, 64)
		if err != nil {
			log.Printf("lyraUpdateIndex: strconvParseFloat error: %v\n\n", err)
		}
	}
}

func updateIndex(res map[string]interface{}) {
	channel, ok := res["channel"].(string)
	if !ok {
		log.Printf("updateIndex: unable to convert response 'channel' to string: %v\n\n", reflect.TypeOf(res["channel"]))
		return
	}

	data, ok := res["data"].(map[string]interface{})
	if !ok {
		log.Printf("updateIndex: unable to cast response to type map[string]interface{}\n\n")
		return
	}
	// if reflect.TypeOf(data["price"]) == nil { //catch ping response, inappropriate to catch here, should fix later
	// 	return
	// }

	asset := strings.TrimPrefix(channel, "index:")
	// fmt.Printf("asset: %v\n\n", asset)

	priceStr, ok := data["price"].(string)
	if !ok {
		log.Printf("updateIndex: unable to cast field to type string: %v\n\n", reflect.TypeOf(data["price"]))
		return
	}

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		log.Printf("updateIndex: error converting string to float64: %v\n\n", err)
		return
	}

	AevoIndex[asset] = price
	// fmt.Printf("index: %+v\n\n", Index)
}

func findApy(expiry string, relProfit float64) float64 {
	ts, err := time.Parse("02Jan06", expiry)
	if err != nil {
		log.Printf("findApy: error parsing expiry to timestamp: %v\n\n", err)
		return 0.0
	}
	timestamp := float64(ts.Unix())
	now := float64(time.Now().Unix())

	apy := math.Pow(1.0+relProfit, 365/math.Ceil((1+timestamp-now)/86400))
	// apy := 365/math.Ceil((1+timestamp-now)/86400) * relProfit

	return apy
}

func updateArbTables(asset string) {
	index := AevoIndex[asset]

	for key, orderbook := range Orderbooks {
		components := strings.Split(key, "-")
		expiry := components[1]
		strike, err := strconv.ParseFloat(components[2], 64)
		if err != nil {
			fmt.Printf("updateArbTables: unable to convert strike string to float64: %v\n", err)
			continue
		}
		optionType := components[3]

		var keyTrim string
		var key2 string
		if optionType == "C" {
			var found bool
			keyTrim, found = strings.CutSuffix(key, "-C")
			if !found {
				continue
			}
			key2 = keyTrim + "-P"
		} else {
			continue
		}

		orderbook2, exists := Orderbooks[key2]
		if !exists {
			continue
		}
		//  abs((index + put) - (strike + call))
		var absProfit float64
		var callBid float64
		var putAsk float64
		if len(orderbook.Bids) > 0 && len(orderbook2.Asks) > 0 {
			callBid = orderbook.Bids[0].Price
			putAsk = orderbook2.Asks[0].Price
			absProfit = math.Abs((index + putAsk) - (strike + callBid))
			relProfit := absProfit / (index + putAsk + callBid) * 100
			apy := findApy(expiry, relProfit)

			if callBid+strike > putAsk+index {
				ArbTables[keyTrim] = &ArbTable{
					Asset:     asset,
					Expiry:    expiry,
					Strike:    strike,
					Bids:      orderbook.Bids,
					Asks:      orderbook2.Asks,
					BidType:   "C",
					AskType:   "P",
					AbsProfit: absProfit,
					RelProfit: relProfit,
					Apy:       apy,
				}
			}
		}

		var callAsk float64
		var putBid float64
		if len(orderbook.Asks) > 0 && len(orderbook2.Bids) > 0 {
			callAsk = orderbook.Asks[0].Price
			putBid = orderbook2.Bids[0].Price
			thisProfit := math.Abs((index + putBid) - (strike + callAsk))
			relProfit := thisProfit / (index + callAsk + putBid) * 100
			apy := findApy(expiry, relProfit)

			if callAsk+strike < putBid+index && thisProfit > absProfit {
				ArbTables[keyTrim] = &ArbTable{
					Asset:     asset,
					Expiry:    expiry,
					Strike:    strike,
					Bids:      orderbook2.Bids,
					Asks:      orderbook.Asks,
					BidType:   "P",
					AskType:   "C",
					AbsProfit: thisProfit,
					RelProfit: relProfit,
					Apy:       apy,
				}
			}
		}

	}
}

func wssRead(ctx context.Context, c *websocket.Conn) []byte {
	_, raw, err := c.Read(ctx)
	if err != nil {
		log.Printf("wssRead: read error: %v, response: %v", err, raw)
	}

	return raw //return error as well?
}

func lyraWssReadLoop(ctx context.Context, c *websocket.Conn) {
	var raw []byte
	var res map[string]interface{}

	for {
		raw = wssRead(ctx, c)

		err := json.Unmarshal(raw, &res)
		if err != nil {
			log.Printf("lyraWssReadLoop: error unmarshaling orderbookRaw: %v\n\n", err)
			continue
		}

		params, ok := res["params"].(map[string]interface{})
		if !ok {
			log.Printf("lyraWssReadLoop: unable to convert res['params'] to map[string]interface{}: (raw response): %v\n\n", string(raw))
			continue
		}

		data, ok := params["data"].(map[string]interface{})
		channel, chanOk := params["channel"].(string)
		if !ok || !chanOk {
			log.Printf("lyraWssReadLoop: unable to convert params['data'] to map[string]interface{} or params['channel'] to string: (raw response): %v\n dataOk: %v\nchannelOk: %v\n\n", string(raw), ok, chanOk)
			continue
		}
		// fmt.Printf("%+v\n\n", res)

		if strings.Contains(channel, "orderbook") {
			lyraUpdateOrderbooks(data)
		}
		if strings.Contains(channel, "spot_feed") {
			lyraUpdateIndex(data)
			fmt.Printf("%+v\n\n", LyraIndex["ETH"])
		}
	}
}

func wssReadLoop(ctx context.Context, c *websocket.Conn) { //add exit condition, add ping or use Reader instead of Read to automatically manage ping, disconnect, etc
	var raw []byte
	var res map[string]interface{}
	for {
		raw = wssRead(ctx, c)

		err := json.Unmarshal(raw, &res)
		if err != nil {
			log.Printf("aevoWssReadLoop: error unmarshaling orderbookRaw: %v\n\n", err)
			continue
		}

		channel, ok := res["channel"].(string)
		if !ok {
			log.Printf("aevoWssReadLoop: unable to convert response 'channel' to string\n\n")
			continue
		}

		if strings.Contains(channel, "orderbook") {
			updateOrderbooks(res)
		}

		if strings.Contains(channel, "index") {
			updateIndex(res)
			updateArbTables("ETH")
		}
	}
}

func wssPingLoop(ctx context.Context, c *websocket.Conn) {
	data := struct {
		Id int    `json:"id"`
		Op string `json:"op"`
	}{
		1,
		"ping",
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		log.Printf("wssPingLoop: json marshal error: %v", err)
	}

	for {
		err = c.Write(ctx, 1, jsonData)
		if err != nil {
			log.Printf("wssPingLoop: write error: %v\n", err)
		}
		log.Printf("Sent ping\n")
		time.Sleep(time.Minute)
	}
}

func lyraWssReqLoop(ctx context.Context, c *websocket.Conn) {
	for {
		assets := []string{"ETH"}
		markets := lyraMarkets("ETH")
		instruments := lyraInstruments(markets)
		fmt.Printf("Lyra number of instruments: %v\n\n", len(instruments))

		lyraWssReqOrderbook(instruments, ctx, c)
		log.Printf("Requested Lyra Orderbooks")
		lyraWssReqIndex(assets, ctx, c)
		log.Printf("Requested Lyra Index")

		time.Sleep(time.Minute * 10)
	}
}

func wssReqLoop(ctx context.Context, c *websocket.Conn) {
	for {
		assets := []string{"ETH"}
		markets := markets("ETH")
		instruments := instruments(markets)
		fmt.Printf("Aevo number of instruments: %v\n\n", len(instruments))

		wssReqOrderbook(instruments, ctx, c)
		log.Printf("Requested Aevo Orderbooks")
		wssReqIndex(assets, ctx, c)
		log.Printf("Requested Aevo Index")

		time.Sleep(time.Minute * 10)
	}
}

func lyraWss() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, res, err := websocket.Dial(ctx, LyraWss, nil)
	if err != nil {
		log.Fatalf("Dial error: %v", err)
	}
	fmt.Printf("%v\n\n", res)
	defer c.Close(websocket.StatusNormalClosure, "")
	defer c.CloseNow()

	go lyraWssReqLoop(ctx, c)
	lyraWssReadLoop(ctx, c)
}

func aevoWss() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, res, err := websocket.Dial(ctx, AevoWss, nil)
	if err != nil {
		log.Fatalf("Dial error: %v", err)
	}
	fmt.Printf("%v\n\n", res)
	defer c.Close(websocket.StatusNormalClosure, "")
	defer c.CloseNow()

	// wssReqOrderbook(instruments, ctx, c)
	// wssReqIndex(assets, ctx, c)
	// go wssPingLoop(ctx, c)
	go wssReqLoop(ctx, c)
	wssReadLoop(ctx, c)
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	tmpl.Execute(w, nil)
}

func arbTableHandler(w http.ResponseWriter, r *http.Request) {
	arbTablesSlice := make([]*ArbTable, len(ArbTables))
	i := 0
	for _, table := range ArbTables {
		arbTablesSlice[i] = table
		i++
	}
	sort.Slice(arbTablesSlice, func(i, j int) bool { return arbTablesSlice[i].Apy > arbTablesSlice[j].Apy })

	responseStr := ""
	for _, value := range arbTablesSlice {
		responseStr += fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			value.Expiry,
			strconv.FormatFloat(value.Strike, 'f', 3, 64),
			value.BidType,
			strconv.FormatFloat(value.Bids[0].Price, 'f', 3, 64),
			value.AskType,
			strconv.FormatFloat(value.Asks[0].Price, 'f', 3, 64),
			strconv.FormatFloat(value.AbsProfit, 'f', 3, 64),
			strconv.FormatFloat(value.RelProfit, 'f', 3, 64),
			strconv.FormatFloat(value.Apy, 'f', 3, 64),
		)
	}

	fmt.Fprint(w, responseStr)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	responseStr := ""
	for key, value := range AevoIndex {
		responseStr += fmt.Sprintf(`<h3>%s: %s</h3>`, key, strconv.FormatFloat(value, 'f', 3, 64))
	}
	fmt.Fprint(w, responseStr)
}

func main() {
	lyraWss()
	// go aevoWss()

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/update-table", arbTableHandler)
	http.HandleFunc("/update-index", indexHandler)
	fmt.Println("Server starting on http://localhost:8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
