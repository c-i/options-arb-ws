package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"text/template"
	"time"

	"nhooyr.io/websocket"
)

const AevoHttp string = "https://api.aevo.xyz"
const AevoWss string = "wss://ws.aevo.xyz"
const LyraHttp string = "https://api.lyra.finance"
const LyraWss string = "wss://api.lyra.finance/ws"

type connData struct {
	Ctx    context.Context
	Conn   *websocket.Conn
	Cancel context.CancelFunc
}

type wssData struct {
	Op   string   `json:"op"`
	Data []string `json:"data"`
}

type Order struct {
	Price    float64
	Amount   float64
	Iv       float64
	Exchange string
}

type OrderbookData struct {
	Bids        map[string][]Order
	Asks        map[string][]Order
	LastUpdated float64
}

type ArbTable struct {
	Asset       string
	Expiry      string
	Strike      float64
	Bids        []Order
	Asks        []Order
	BidType     string
	AskType     string
	BidExchange string
	AskExchange string
	AbsProfit   float64
	RelProfit   float64
	Apy         float64
}

type ArbTablesContainer struct {
	Mu        sync.Mutex
	ArbTables map[string]*ArbTable
}

type IndexContainer struct {
	Mu    sync.Mutex
	Index map[string]float64
}

// pointer seems like a bad idea but makes assignment of elements easier
var Orderbooks = make(map[string]*OrderbookData) //key: e.g. "ETH-02JAN06-3000-C"
var ArbContainer = ArbTablesContainer{ArbTables: make(map[string]*ArbTable)}
var AevoIndex = IndexContainer{Index: make(map[string]float64)}
var LyraIndex = IndexContainer{Index: make(map[string]float64)}

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

		unpackedOrders = append(unpackedOrders, Order{price, amount, iv, exchange})
	}

	return unpackedOrders, nil
}

func wssRead(ctx context.Context, c *websocket.Conn) ([]byte, error) {
	_, raw, err := c.Read(ctx)
	if err != nil {
		return raw, fmt.Errorf("wssRead: read error: %v\n(response): %v", err, raw)
	}

	return raw, nil //return error as well?
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

func dialWss(url string) (context.Context, *websocket.Conn, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	c, res, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		log.Fatalf("Dial error: %v", err)
	}
	fmt.Printf("%v\n\n", res)

	return ctx, c, cancel
}

func mainEventLoop(connections map[string]connData) {
	// maxTime := time.Second * 0
	for {
		// start := time.Now()
		aevoWssRead(connections["aevo"].Ctx, connections["aevo"].Conn)
		lyraWssRead(connections["lyra"].Ctx, connections["lyra"].Conn)
		updateArbTables("ETH")
		// duration := time.Since(start)
		// if duration > maxTime && duration < time.Second*2 {
		// 	maxTime = duration
		// }
		// log.Printf("loop time: %v\nmax time: %v\n\n", duration, maxTime)
	}
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.ParseFiles("templates/index.html"))
	tmpl.Execute(w, nil)
}

func arbTableHandler(w http.ResponseWriter, r *http.Request) {
	ArbContainer.Mu.Lock()
	defer ArbContainer.Mu.Unlock()

	arbTablesSlice := make([]*ArbTable, len(ArbContainer.ArbTables)) //converting to slice to sort by apy
	i := 0
	for _, table := range ArbContainer.ArbTables {
		arbTablesSlice[i] = table
		i++
	}
	sort.Slice(arbTablesSlice, func(i, j int) bool { return arbTablesSlice[i].Apy > arbTablesSlice[j].Apy })

	responseStr := ""
	for _, value := range arbTablesSlice {
		responseStr += fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			value.Expiry,
			strconv.FormatFloat(value.Strike, 'f', 3, 64),
			value.BidExchange,
			value.BidType,
			strconv.FormatFloat(value.Bids[0].Price, 'f', 3, 64),
			value.AskExchange,
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
	AevoIndex.Mu.Lock()
	LyraIndex.Mu.Lock()
	defer AevoIndex.Mu.Unlock()
	defer LyraIndex.Mu.Unlock()
	responseStr := ""
	text := ""
	for key, value := range AevoIndex.Index {
		text += fmt.Sprintf(`%s: %s &nbsp;&nbsp;&nbsp;`, key, strconv.FormatFloat(value, 'f', 3, 64))
		responseStr += fmt.Sprintf(`<h3>Aevo:  %s</h3>`, text)
	}

	text = ""
	for key, value := range LyraIndex.Index {
		text += fmt.Sprintf(`%s: %s &nbsp;&nbsp;&nbsp;`, key, strconv.FormatFloat(value, 'f', 3, 64))
		responseStr += fmt.Sprintf(`<h3>Lyra:  %s</h3>`, text)
	}
	fmt.Fprint(w, responseStr)
}

func main() {
	aevoCtx, aevoConn, aevoCancel := dialWss(AevoWss)
	lyraCtx, lyraConn, lyraCancel := dialWss(LyraWss)
	connections := map[string]connData{
		"aevo": {aevoCtx, aevoConn, aevoCancel},
		"lyra": {lyraCtx, lyraConn, lyraCancel},
	}
	defer aevoCancel()
	defer aevoConn.Close(websocket.StatusNormalClosure, "")
	defer aevoConn.CloseNow()
	defer lyraCancel()
	defer lyraConn.Close(websocket.StatusNormalClosure, "")
	defer lyraConn.CloseNow()

	go aevoWssReqLoop(aevoCtx, aevoConn)
	go lyraWssReqLoop(lyraCtx, lyraConn)

	go mainEventLoop(connections)

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/update-table", arbTableHandler)
	http.HandleFunc("/update-index", indexHandler)
	fmt.Println("Server starting on http://localhost:8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
