package vm

import (
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/consensus"
	"github.com/tomochain/tomochain/core/state"
	"github.com/tomochain/tomochain/core/types"
	"github.com/tomochain/tomochain/params"
	"github.com/tomochain/tomochain/tomox/tradingstate"
	"github.com/tomochain/tomochain/tomoxlending"
	"github.com/tomochain/tomochain/tomoxlending/lendingstate"
)

type tomoxLendingOrder struct {
	lending        *tomoxlending.Lending
	header         *types.Header
	coinbase       common.Address
	chain          consensus.ChainContext
	statedb        *state.StateDB
	lendingStateDb *lendingstate.LendingStateDB
	tradingStateDb *tradingstate.TradingStateDB
}

func (t *tomoxLendingOrder) RequiredGas(input []byte) uint64 {
	return params.TomoXPriceGas
}

func (t *tomoxLendingOrder) Run(input []byte) ([]byte, error) {
	// input includes baseTokenAddress, quoteTokenAddress
	if t.lending != nil && len(input) == 64 {
		//base := common.BytesToAddress(input[12:32]) // 20 bytes from 13-32
		//quote := common.BytesToAddress(input[44:])  // 20 bytes from 45-64
		//price := t.tradingStateDB.GetLastPrice(tradingstate.GetTradingOrderBookHash(base, quote))
		//if price != nil {
		//	log.Debug("Run GetLastPrice", "base", base.Hex(), "quote", quote.Hex(), "price", price)
		//	return common.LeftPadBytes(price.Bytes(), TomoXPriceNumberOfBytesReturn), nil
		//}
		order := &lendingstate.LendingItem{}
		lendingOrderBook := lendingstate.GetLendingOrderBookHash(order.LendingToken, order.Term)
		t.lending.CommitOrder(t.header, t.coinbase, t.chain, t.statedb, t.lendingStateDb, t.tradingStateDb, lendingOrderBook, order)
	}
	return common.LeftPadBytes([]byte{}, TomoXPriceNumberOfBytesReturn), nil
}

func (t *tomoxLendingOrder) SetInfo(lending *tomoxlending.Lending, header *types.Header, coinbase common.Address, chain consensus.ChainContext, stateDb *state.StateDB, lendingStateDb *lendingstate.LendingStateDB, tradingStateDb *tradingstate.TradingStateDB) {
	t.lending = lending
	t.header = header
	t.coinbase = coinbase
	t.chain = chain
	if tradingStateDb != nil {
		t.tradingStateDb = tradingStateDb.Copy()
	} else {
		t.tradingStateDb = nil
	}
	if lendingStateDb != nil {
		t.lendingStateDb = lendingStateDb.Copy()
	} else {
		t.lendingStateDb = nil
	}
	if stateDb != nil {
		t.statedb = stateDb.Copy()
	} else {
		t.statedb = nil
	}
}
