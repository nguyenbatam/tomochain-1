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
	"math/big"
	"strconv"
	"strings"
)

type tomoxLendingOrder struct {
	lending         *tomoxlending.Lending
	header          *types.Header
	coinbase        common.Address
	chain           consensus.ChainContext
	statedb         *state.StateDB
	lendingStateDb  *lendingstate.LendingStateDB
	tradingStateDb  *tradingstate.TradingStateDB
	tokenDecimals   map[common.Address]*big.Int
	contractAddr    common.Address
	matchingResults map[common.Hash]lendingstate.MatchingResult
}

func (t *tomoxLendingOrder) RequiredGas(input []byte) uint64 {
	return params.TomoXPriceGas
}

func (t *tomoxLendingOrder) Run(input []byte) ([]byte, error) {
	// input includes baseTokenAddress, quoteTokenAddress
	if t.lending != nil && len(input) == 416 {
		quantity := new(big.Int).SetBytes(input[0:32])
		interest := new(big.Int).SetBytes(input[32:64])
		side := strings.TrimSpace(string(input[64:96]))
		_type := strings.TrimSpace(string(input[96:128]))
		lendingToken := common.BytesToAddress(input[140:160])
		collateralToken := common.BytesToAddress(input[172:192])
		autoTopUp, _ := strconv.ParseBool(string(input[193:224]))
		status := strings.TrimSpace(string(input[224:256]))
		relayer := common.BytesToAddress(input[268:288])
		term := new(big.Int).SetBytes(input[288:320]).Uint64()
		hash := common.BytesToHash(input[320:352])
		lendingId := new(big.Int).SetBytes(input[352:384]).Uint64()
		lendingTradeId := new(big.Int).SetBytes(input[384:416]).Uint64()
		// time , filledAmount, txhash, extradata is empty
		order := &lendingstate.LendingItem{
			UserAddress:     t.contractAddr,
			Nonce:           new(big.Int).SetUint64(t.statedb.GetNonce(t.contractAddr)),
			Quantity:        quantity,
			Interest:        interest,
			Side:            side,
			Type:            _type,
			LendingToken:    lendingToken,
			CollateralToken: collateralToken,
			AutoTopUp:       autoTopUp,
			Status:          status,
			Relayer:         relayer,
			Term:            term,
			Hash:            hash,
			LendingId:       lendingId,
			LendingTradeId:  lendingTradeId,
		}
		if err := order.VerifyEVMLendingItem(t.statedb); err != nil {
			return common.LeftPadBytes([]byte{}, TomoXPriceNumberOfBytesReturn), err
		}
		lendingOrderBook := lendingstate.GetLendingOrderBookHash(order.LendingToken, order.Term)

		originalOrder := &lendingstate.LendingItem{}
		*originalOrder = *order
		originalOrder.Quantity = lendingstate.CloneBigInt(order.Quantity)

		trades, rejects, err := t.lending.ApplyEVMOrder(t.tokenDecimals, t.header, t.coinbase, t.chain, t.statedb, t.lendingStateDb, t.tradingStateDb, lendingOrderBook, order)
		if err != nil {
			return common.LeftPadBytes([]byte{}, TomoXPriceNumberOfBytesReturn), err
		}
		originalOrder.LendingId = order.LendingId
		originalOrder.ExtraData = order.ExtraData
		t.matchingResults[lendingstate.GetLendingCacheKey(order)] = lendingstate.MatchingResult{
			Trades:  trades,
			Rejects: rejects,
		}
	}
	return common.LeftPadBytes([]byte{}, TomoXPriceNumberOfBytesReturn), nil
}

func (t *tomoxLendingOrder) SetInfo(tokenDecimals map[common.Address]*big.Int, lending *tomoxlending.Lending, header *types.Header, coinbase common.Address, chain consensus.ChainContext, stateDb *state.StateDB, lendingStateDb *lendingstate.LendingStateDB, tradingStateDb *tradingstate.TradingStateDB, contractAddr common.Address) {
	t.lending = lending
	t.header = header
	t.coinbase = coinbase
	t.chain = chain
	t.tokenDecimals = tokenDecimals
	t.contractAddr = contractAddr
	t.matchingResults = map[common.Hash]lendingstate.MatchingResult{}
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
