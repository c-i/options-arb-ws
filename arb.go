package main

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

func findApy(expiry string, relProfit float64) float64 {
	ts, err := time.Parse("02Jan06", expiry)
	if err != nil {
		log.Printf("findApy: error parsing expiry to timestamp: %v\n\n", err)
		return 0.0
	}
	timestamp := float64(ts.Unix())
	now := float64(time.Now().Unix())

	apy := math.Pow(1.0+(relProfit/100), 365/math.Ceil((1+timestamp-now)/86400)) * 100
	// apy := 365/math.Ceil((1+timestamp-now)/86400) * relProfit

	return apy
}

func updateArbTable(asset string, key string, callOrderbook *OrderbookData, putOrderbook *OrderbookData, expiry string, strike float64) {
	ArbContainer.Mu.Lock()
	defer ArbContainer.Mu.Unlock()

	//  abs((index + put) - (strike + call))
	var absProfit float64
	var callBid float64
	var putAsk float64
	var index float64
	if len(callOrderbook.Bids) > 0 && len(putOrderbook.Asks) > 0 {
		callBid = callOrderbook.Bids[0].Price
		putAsk = putOrderbook.Asks[0].Price

		index = AevoIndex.Index[asset]

		_, exists := LyraIndex.Index["ETH"]
		if putOrderbook.Asks[0].Exchange == "lyra" && exists {
			index = LyraIndex.Index[asset]
		}

		absProfit = math.Abs((index + putAsk) - (strike + callBid))
		relProfit := absProfit / (index + putAsk + callBid) * 100
		apy := findApy(expiry, relProfit)

		if callBid+strike > putAsk+index {
			ArbContainer.ArbTables[key] = &ArbTable{
				Asset:       asset,
				Expiry:      expiry,
				Strike:      strike,
				Bids:        callOrderbook.Bids,
				Asks:        putOrderbook.Asks,
				BidType:     "C",
				AskType:     "P",
				BidExchange: callOrderbook.Bids[0].Exchange,
				AskExchange: putOrderbook.Asks[0].Exchange,
				AbsProfit:   absProfit,
				RelProfit:   relProfit,
				Apy:         apy,
			}
		}
	}

	var callAsk float64
	var putBid float64
	if len(callOrderbook.Asks) > 0 && len(putOrderbook.Bids) > 0 {
		callAsk = callOrderbook.Asks[0].Price
		putBid = putOrderbook.Bids[0].Price
		thisProfit := math.Abs((index + putBid) - (strike + callAsk))
		relProfit := thisProfit / (index + callAsk + putBid) * 100
		apy := findApy(expiry, relProfit)

		if callAsk+strike < putBid+index && thisProfit > absProfit {
			ArbContainer.ArbTables[key] = &ArbTable{
				Asset:       asset,
				Expiry:      expiry,
				Strike:      strike,
				Bids:        putOrderbook.Bids,
				Asks:        callOrderbook.Asks,
				BidType:     "P",
				AskType:     "C",
				BidExchange: putOrderbook.Bids[0].Exchange,
				AskExchange: callOrderbook.Asks[0].Exchange,
				AbsProfit:   thisProfit,
				RelProfit:   relProfit,
				Apy:         apy,
			}
		}
	}
}

func updateArbTables(asset string) {
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

		updateArbTable(asset, keyTrim, orderbook, orderbook2, expiry, strike)

	}
}
