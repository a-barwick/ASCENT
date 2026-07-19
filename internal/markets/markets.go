// Package markets implements a deterministic GTC limit-order book.
package markets

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var (
	ErrInvalidMarket        = errors.New("invalid market")
	ErrInvalidOrder         = errors.New("invalid limit order")
	ErrDuplicateOrder       = errors.New("duplicate order")
	ErrUnknownOrder         = errors.New("unknown order")
	ErrOrderClosed          = errors.New("order is closed")
	ErrOrderOwnership       = errors.New("order belongs to another company")
	ErrInsufficientCapacity = errors.New("insufficient funds or inventory for reservation")
	ErrQuoteOverflow        = errors.New("quote amount overflow")
	ErrNonIntegralQuote     = errors.New("quote amount is not an integer minor unit")
	ErrSelfTrade            = errors.New("self trade is not allowed")
)

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

type OrderStatus string

const (
	OrderOpen            OrderStatus = "open"
	OrderPartiallyFilled OrderStatus = "partially_filled"
	OrderFilled          OrderStatus = "filled"
	OrderCanceled        OrderStatus = "canceled"
)

// Market prices are currency minor units per QuantityScale product units.
// LotSize must divide submitted quantities and make each allowed price produce
// an integral quote amount.
type Market struct {
	ID            string `json:"id"`
	ProductID     string `json:"productId"`
	Currency      string `json:"currency"`
	LocationID    string `json:"locationId"`
	QuantityScale int64  `json:"quantityScale"`
	LotSize       int64  `json:"lotSize"`
}

type OrderRequest struct {
	ID          string `json:"id"`
	OperationID string `json:"operationId"`
	CompanyID   string `json:"companyId"`
	Side        Side   `json:"side"`
	Price       int64  `json:"priceMinor"`
	Quantity    int64  `json:"quantity"`
	Sequence    uint64 `json:"sequence"`
}

type Order struct {
	ID                string      `json:"id"`
	OperationID       string      `json:"operationId"`
	MarketID          string      `json:"marketId"`
	CompanyID         string      `json:"companyId"`
	Side              Side        `json:"side"`
	Price             int64       `json:"priceMinor"`
	Quantity          int64       `json:"quantity"`
	RemainingQuantity int64       `json:"remainingQuantity"`
	ReservedRemaining int64       `json:"reservedRemaining"`
	Sequence          uint64      `json:"sequence"`
	Status            OrderStatus `json:"status"`
}

type Trade struct {
	ID           string `json:"id"`
	OperationID  string `json:"operationId"`
	MarketID     string `json:"marketId"`
	BuyOrderID   string `json:"buyOrderId"`
	SellOrderID  string `json:"sellOrderId"`
	BuyerID      string `json:"buyerId"`
	SellerID     string `json:"sellerId"`
	Price        int64  `json:"priceMinor"`
	Quantity     int64  `json:"quantity"`
	QuoteAmount  int64  `json:"quoteAmountMinor"`
	Sequence     uint64 `json:"sequence"`
	MakerOrderID string `json:"makerOrderId"`
	TakerOrderID string `json:"takerOrderId"`
}

type ReservationAsset string

const (
	ReservationQuote ReservationAsset = "quote"
	ReservationBase  ReservationAsset = "base"
)

// ReservationEffect describes how much of an order reservation was consumed
// by settlement and how much price improvement or cancellation released.
type ReservationEffect struct {
	OrderID   string           `json:"orderId"`
	CompanyID string           `json:"companyId"`
	Asset     ReservationAsset `json:"asset"`
	Consumed  int64            `json:"consumed"`
	Released  int64            `json:"released"`
}

type Execution struct {
	Order              Order               `json:"order"`
	Trades             []Trade             `json:"trades"`
	ReservationEffects []ReservationEffect `json:"reservationEffects"`
}

type CancelResult struct {
	Order             Order             `json:"order"`
	ReservationEffect ReservationEffect `json:"reservationEffect"`
}

type Book struct {
	market            Market
	orders            map[string]Order
	trades            []Trade
	nextTradeSequence uint64
}

func NewBook(market Market) (Book, error) {
	if market.ID == "" || market.ProductID == "" || market.Currency == "" || market.LocationID == "" ||
		market.QuantityScale <= 0 || market.LotSize <= 0 {
		return Book{}, ErrInvalidMarket
	}
	if market.QuantityScale%market.LotSize != 0 {
		return Book{}, fmt.Errorf("%w: lot size must divide quantity scale", ErrInvalidMarket)
	}
	return Book{
		market:            market,
		orders:            make(map[string]Order),
		nextTradeSequence: 1,
	}, nil
}

func (b Book) Market() Market {
	return b.market
}

// Submit reserves against availableCapacity supplied from authoritative caller
// state. Buy capacity is quote currency minor units; sell capacity is product
// quantity. The returned Book is the only mutated value.
func (b Book) Submit(request OrderRequest, availableCapacity int64) (Book, Execution, error) {
	if err := b.validateOrder(request); err != nil {
		return Book{}, Execution{}, err
	}
	if _, exists := b.orders[request.ID]; exists {
		return Book{}, Execution{}, fmt.Errorf("%w: %q", ErrDuplicateOrder, request.ID)
	}

	reservation, err := b.reservationFor(request.Side, request.Price, request.Quantity)
	if err != nil {
		return Book{}, Execution{}, err
	}
	if availableCapacity < reservation {
		return Book{}, Execution{}, fmt.Errorf("%w: need %d, have %d", ErrInsufficientCapacity, reservation, availableCapacity)
	}

	next := b.clone()
	next.orders[request.ID] = Order{
		ID:                request.ID,
		OperationID:       request.OperationID,
		MarketID:          b.market.ID,
		CompanyID:         request.CompanyID,
		Side:              request.Side,
		Price:             request.Price,
		Quantity:          request.Quantity,
		RemainingQuantity: request.Quantity,
		ReservedRemaining: reservation,
		Sequence:          request.Sequence,
		Status:            OrderOpen,
	}

	execution := Execution{}
	for {
		buy, hasBuy := next.bestOrder(SideBuy)
		sell, hasSell := next.bestOrder(SideSell)
		if !hasBuy || !hasSell || buy.Price < sell.Price {
			break
		}
		if buy.CompanyID == sell.CompanyID {
			return Book{}, Execution{}, fmt.Errorf("%w: company %q", ErrSelfTrade, buy.CompanyID)
		}

		quantity := min(buy.RemainingQuantity, sell.RemainingQuantity)
		maker, taker := makerAndTaker(buy, sell)
		price := maker.Price
		quoteAmount, err := b.quoteAmount(price, quantity)
		if err != nil {
			return Book{}, Execution{}, err
		}
		buyLimitAmount, err := b.quoteAmount(buy.Price, quantity)
		if err != nil {
			return Book{}, Execution{}, err
		}

		tradeSequence := next.nextTradeSequence
		next.nextTradeSequence++
		trade := Trade{
			ID:           fmt.Sprintf("%s-trade-%06d", b.market.ID, tradeSequence),
			OperationID:  request.OperationID,
			MarketID:     b.market.ID,
			BuyOrderID:   buy.ID,
			SellOrderID:  sell.ID,
			BuyerID:      buy.CompanyID,
			SellerID:     sell.CompanyID,
			Price:        price,
			Quantity:     quantity,
			QuoteAmount:  quoteAmount,
			Sequence:     tradeSequence,
			MakerOrderID: maker.ID,
			TakerOrderID: taker.ID,
		}
		next.trades = append(next.trades, trade)
		execution.Trades = append(execution.Trades, trade)
		execution.ReservationEffects = append(execution.ReservationEffects,
			ReservationEffect{
				OrderID:   buy.ID,
				CompanyID: buy.CompanyID,
				Asset:     ReservationQuote,
				Consumed:  quoteAmount,
				Released:  buyLimitAmount - quoteAmount,
			},
			ReservationEffect{
				OrderID:   sell.ID,
				CompanyID: sell.CompanyID,
				Asset:     ReservationBase,
				Consumed:  quantity,
			},
		)

		buy.RemainingQuantity -= quantity
		buy.ReservedRemaining -= buyLimitAmount
		buy.Status = statusAfterFill(buy)
		sell.RemainingQuantity -= quantity
		sell.ReservedRemaining -= quantity
		sell.Status = statusAfterFill(sell)
		next.orders[buy.ID] = buy
		next.orders[sell.ID] = sell
	}

	execution.Order = next.orders[request.ID]
	return next, execution, nil
}

func (b Book) Cancel(companyID, orderID string) (Book, CancelResult, error) {
	order, exists := b.orders[orderID]
	if !exists {
		return Book{}, CancelResult{}, fmt.Errorf("%w: %q", ErrUnknownOrder, orderID)
	}
	if order.CompanyID != companyID {
		return Book{}, CancelResult{}, ErrOrderOwnership
	}
	if !active(order) {
		return Book{}, CancelResult{}, fmt.Errorf("%w: %q", ErrOrderClosed, orderID)
	}

	effect := ReservationEffect{
		OrderID:   order.ID,
		CompanyID: order.CompanyID,
		Released:  order.ReservedRemaining,
	}
	if order.Side == SideBuy {
		effect.Asset = ReservationQuote
	} else {
		effect.Asset = ReservationBase
	}
	order.ReservedRemaining = 0
	order.Status = OrderCanceled

	next := b.clone()
	next.orders[order.ID] = order
	return next, CancelResult{Order: order, ReservationEffect: effect}, nil
}

func (b Book) Order(id string) (Order, bool) {
	order, exists := b.orders[id]
	return order, exists
}

func (b Book) Orders() []Order {
	result := make([]Order, 0, len(b.orders))
	for _, order := range b.orders {
		result = append(result, order)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Sequence != result[j].Sequence {
			return result[i].Sequence < result[j].Sequence
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (b Book) Trades() []Trade {
	return append([]Trade(nil), b.trades...)
}

func (b Book) ReservedQuote(companyID string) (int64, error) {
	var total int64
	for _, order := range b.orders {
		if order.CompanyID != companyID || order.Side != SideBuy || !active(order) {
			continue
		}
		next, ok := checkedAdd(total, order.ReservedRemaining)
		if !ok {
			return 0, ErrQuoteOverflow
		}
		total = next
	}
	return total, nil
}

func (b Book) ReservedBase(companyID string) (int64, error) {
	var total int64
	for _, order := range b.orders {
		if order.CompanyID != companyID || order.Side != SideSell || !active(order) {
			continue
		}
		next, ok := checkedAdd(total, order.ReservedRemaining)
		if !ok {
			return 0, ErrQuoteOverflow
		}
		total = next
	}
	return total, nil
}

func (b Book) validateOrder(request OrderRequest) error {
	if request.ID == "" || request.OperationID == "" || request.CompanyID == "" ||
		(request.Side != SideBuy && request.Side != SideSell) ||
		request.Price <= 0 || request.Quantity <= 0 || request.Sequence == 0 {
		return ErrInvalidOrder
	}
	if request.Quantity%b.market.LotSize != 0 {
		return fmt.Errorf("%w: quantity must be a multiple of lot size", ErrInvalidOrder)
	}
	if _, err := b.quoteAmount(request.Price, b.market.LotSize); err != nil {
		return err
	}
	return nil
}

func (b Book) reservationFor(side Side, price, quantity int64) (int64, error) {
	if side == SideSell {
		return quantity, nil
	}
	return b.quoteAmount(price, quantity)
}

func (b Book) quoteAmount(price, quantity int64) (int64, error) {
	product, ok := checkedMultiply(price, quantity)
	if !ok {
		return 0, ErrQuoteOverflow
	}
	if product%b.market.QuantityScale != 0 {
		return 0, ErrNonIntegralQuote
	}
	return product / b.market.QuantityScale, nil
}

func (b Book) bestOrder(side Side) (Order, bool) {
	var best Order
	found := false
	for _, candidate := range b.orders {
		if candidate.Side != side || !active(candidate) {
			continue
		}
		if !found || better(candidate, best, side) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func (b Book) clone() Book {
	next := Book{
		market:            b.market,
		orders:            make(map[string]Order, len(b.orders)+1),
		trades:            append([]Trade(nil), b.trades...),
		nextTradeSequence: b.nextTradeSequence,
	}
	for id, order := range b.orders {
		next.orders[id] = order
	}
	return next
}

func better(candidate, current Order, side Side) bool {
	if candidate.Price != current.Price {
		if side == SideBuy {
			return candidate.Price > current.Price
		}
		return candidate.Price < current.Price
	}
	if candidate.Sequence != current.Sequence {
		return candidate.Sequence < current.Sequence
	}
	return candidate.ID < current.ID
}

func makerAndTaker(buy, sell Order) (Order, Order) {
	if buy.Sequence < sell.Sequence || (buy.Sequence == sell.Sequence && buy.ID < sell.ID) {
		return buy, sell
	}
	return sell, buy
}

func statusAfterFill(order Order) OrderStatus {
	if order.RemainingQuantity == 0 {
		return OrderFilled
	}
	return OrderPartiallyFilled
}

func active(order Order) bool {
	return order.Status == OrderOpen || order.Status == OrderPartiallyFilled
}

func min(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func checkedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func checkedMultiply(left, right int64) (int64, bool) {
	if left == 0 || right == 0 {
		return 0, true
	}
	if left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}
