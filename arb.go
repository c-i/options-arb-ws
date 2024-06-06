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

func updateArbTable(asset string, key string, callBids []Order, callAsks []Order, putBids []Order, putAsks []Order, expiry string, strike float64) {
	ArbContainer.Mu.Lock()
	LyraIndex.Mu.Lock()
	AevoIndex.Mu.Lock()
	defer LyraIndex.Mu.Unlock()
	defer AevoIndex.Mu.Unlock()
	defer ArbContainer.Mu.Unlock()

	//  abs((index + put) - (strike + call))
	var absProfit float64
	var callBid float64
	var putAsk float64
	var index float64
	if len(callBids) > 0 && len(putAsks) > 0 {
		callBid = callBids[0].Price
		putAsk = putAsks[0].Price

		index = AevoIndex.Index[asset]

		_, exists := LyraIndex.Index[asset]
		if putAsks[0].Exchange == "lyra" && exists {
			index = LyraIndex.Index[asset]
		}

		if index <= 0 { //not a good solution, but checking UpdateIndex doesnt work for some reason, maybe add Index variable to orderbooks struct and use that instead of global index
			return
		}

		absProfit = math.Abs((index + putAsk) - (strike + callBid)) //broken when index is near 0
		relProfit := absProfit / (index + putAsk + callBid) * 100
		apy := findApy(expiry, relProfit)

		if callBid+strike > putAsk+index {
			ArbContainer.ArbTables[key] = &ArbTable{
				Asset:       asset,
				Expiry:      expiry,
				Strike:      strike,
				Bids:        callBids,
				Asks:        putAsks,
				BidType:     "C",
				AskType:     "P",
				BidExchange: callBids[0].Exchange,
				AskExchange: putAsks[0].Exchange,
				AbsProfit:   absProfit,
				RelProfit:   relProfit,
				Apy:         apy,
			}
		}
	}

	var callAsk float64
	var putBid float64
	if len(callAsks) > 0 && len(putBids) > 0 {
		callAsk = callAsks[0].Price
		putBid = putBids[0].Price
		thisProfit := math.Abs((index + putBid) - (strike + callAsk))
		relProfit := thisProfit / (index + callAsk + putBid) * 100
		apy := findApy(expiry, relProfit)

		if callAsk+strike < putBid+index && thisProfit > absProfit {
			ArbContainer.ArbTables[key] = &ArbTable{
				Asset:       asset,
				Expiry:      expiry,
				Strike:      strike,
				Bids:        putBids,
				Asks:        callAsks,
				BidType:     "P",
				AskType:     "C",
				BidExchange: putBids[0].Exchange,
				AskExchange: callAsks[0].Exchange,
				AbsProfit:   thisProfit,
				RelProfit:   relProfit,
				Apy:         apy,
			}
		}
	}
}

func findBestOrders(callOrderbook *OrderbookData, putOrderbook *OrderbookData) (callBids []Order, callAsks []Order, putBids []Order, putAsks []Order) {
	callBidExists := false
	callAskExists := false
	putBidExists := false
	putAskExists := false

	bestCallBids := []Order{{Price: -1}}
	bestPutAsks := []Order{{Price: 100000000}}
	bestCallAsks := []Order{{Price: 100000000}}
	bestPutBids := []Order{{Price: -1}}

	for _, bid := range callOrderbook.Bids {
		// remember to use correct comparison sign based on bid or ask (highest bid lowest ask)
		if len(bid) > 0 { //need to check if each map entry is nonempty, Exists only stays false if all are empty
			callBidExists = true
		} else {
			continue
		}
		if bid[0].Price > bestCallBids[0].Price {
			bestCallBids = bid
		}
	}
	if !callBidExists {
		bestCallBids = []Order{}
	}

	for _, ask := range callOrderbook.Asks {
		if len(ask) > 0 {
			callAskExists = true
		} else {
			continue
		}
		if ask[0].Price < bestCallAsks[0].Price {
			bestCallAsks = ask
		}
	}
	if !callAskExists {
		bestCallAsks = []Order{}
	}

	for _, bid := range putOrderbook.Bids {
		if len(bid) > 0 {
			putBidExists = true
		} else {
			continue
		}
		if bid[0].Price > bestPutBids[0].Price {
			bestPutBids = bid
		}
	}
	if !putBidExists {
		bestPutBids = []Order{}
	}

	for _, ask := range putOrderbook.Asks {
		if len(ask) > 0 {
			putAskExists = true
		} else {
			continue
		}
		if ask[0].Price < bestPutAsks[0].Price {
			bestPutAsks = ask
		}
	}
	if !putAskExists {
		bestPutAsks = []Order{}
	}

	return bestCallBids, bestCallAsks, bestPutBids, bestPutAsks
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

		bestCallBids, bestCallAsks, bestPutBids, bestPutAsks := findBestOrders(orderbook, orderbook2)
		// fmt.Printf("%v\n%v\n%v\n%v\n\n", bestCallBids, bestCallAsks, bestPutBids, bestPutAsks)

		updateArbTable(asset, keyTrim, bestCallBids, bestCallAsks, bestPutBids, bestPutAsks, expiry, strike)

	}
}
