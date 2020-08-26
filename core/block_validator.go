// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"fmt"
	"github.com/tomochain/tomochain"
	"github.com/tomochain/tomochain/accounts/abi"
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/consensus"
	"github.com/tomochain/tomochain/consensus/posv"
	"github.com/tomochain/tomochain/contracts/trc21issuer/contract"
	"github.com/tomochain/tomochain/core/state"
	"github.com/tomochain/tomochain/core/types"
	"github.com/tomochain/tomochain/core/vm"
	"github.com/tomochain/tomochain/log"
	"github.com/tomochain/tomochain/params"
	"github.com/tomochain/tomochain/tomox/tradingstate"
	"github.com/tomochain/tomochain/tomoxlending/lendingstate"
	"math/big"
	"strings"
)

// BlockValidator is responsible for validating block headers, uncles and
// processed state.
//
// BlockValidator implements Validator.
type BlockValidator struct {
	config *params.ChainConfig // Chain configuration options
	bc     *BlockChain         // Canonical block chain
	engine consensus.Engine    // Consensus engine used for validating
}

// NewBlockValidator returns a new block validator which is safe for re-use
func NewBlockValidator(config *params.ChainConfig, blockchain *BlockChain, engine consensus.Engine) *BlockValidator {
	validator := &BlockValidator{
		config: config,
		engine: engine,
		bc:     blockchain,
	}
	return validator
}

// ValidateBody validates the given block's uncles and verifies the the block
// header's transaction and uncle roots. The headers are assumed to be already
// validated at this point.
func (v *BlockValidator) ValidateBody(block *types.Block) error {
	// Check whether the block's known, and if not, that it's linkable
	if v.bc.HasBlockAndFullState(block.Hash(), block.NumberU64()) {
		return ErrKnownBlock
	}
	if !v.bc.HasBlockAndFullState(block.ParentHash(), block.NumberU64()-1) {
		if !v.bc.HasBlock(block.ParentHash(), block.NumberU64()-1) {
			return consensus.ErrUnknownAncestor
		}
		return consensus.ErrPrunedAncestor
	}
	// Header validity is known at this point, check the uncles and transactions
	header := block.Header()
	if err := v.engine.VerifyUncles(v.bc, block); err != nil {
		return err
	}
	if hash := types.CalcUncleHash(block.Uncles()); hash != header.UncleHash {
		return fmt.Errorf("uncle root hash mismatch: have %x, want %x", hash, header.UncleHash)
	}
	if hash := types.DeriveSha(block.Transactions()); hash != header.TxHash {
		return fmt.Errorf("transaction root hash mismatch: have %x, want %x", hash, header.TxHash)
	}
	return nil
}

// ValidateState validates the various changes that happen after a state
// transition, such as amount of used gas, the receipt roots and the state root
// itself. ValidateState returns a database batch if the validation was a success
// otherwise nil and an error is returned.
func (v *BlockValidator) ValidateState(block, parent *types.Block, statedb *state.StateDB, receipts types.Receipts, usedGas uint64) error {
	header := block.Header()
	if block.GasUsed() != usedGas {
		return fmt.Errorf("invalid gas used (remote: %d local: %d)", block.GasUsed(), usedGas)
	}
	// Validate the received block's bloom with the one derived from the generated receipts.
	// For valid blocks this should always validate to true.
	rbloom := types.CreateBloom(receipts)
	if rbloom != header.Bloom {
		return fmt.Errorf("invalid bloom (remote: %x  local: %x)", header.Bloom, rbloom)
	}
	// Tre receipt Trie's root (R = (Tr [[H1, R1], ... [Hn, R1]]))
	receiptSha := types.DeriveSha(receipts)
	if receiptSha != header.ReceiptHash {
		return fmt.Errorf("invalid receipt root hash (remote: %x local: %x)", header.ReceiptHash, receiptSha)
	}
	// Validate the state root against the received state root and throw
	// an error if they don't match.
	if root := statedb.IntermediateRoot(v.config.IsEIP158(header.Number)); header.Root != root {
		return fmt.Errorf("invalid merkle root (remote: %x local: %x)", header.Root, root)
	}
	return nil
}

func (v *BlockValidator) ValidateTradingOrder(tokenDecimals map[common.Address]*big.Int, statedb *state.StateDB, tomoxStatedb *tradingstate.TradingStateDB, txMatchBatch tradingstate.TxMatchBatch, coinbase common.Address, header *types.Header) error {
	posvEngine, ok := v.bc.Engine().(*posv.Posv)
	if posvEngine == nil || !ok {
		return ErrNotPoSV
	}
	tomoXService := posvEngine.GetTomoXService()
	if tomoXService == nil {
		return fmt.Errorf("tomox not found")
	}
	log.Debug("verify matching transaction found a TxMatches Batch", "numTxMatches", len(txMatchBatch.Data))
	tradingResult := map[common.Hash]tradingstate.MatchingResult{}
	for _, txMatch := range txMatchBatch.Data {
		// verify orderItem
		order, err := txMatch.DecodeOrder()
		if err != nil {
			log.Error("transaction match is corrupted. Failed decode order", "err", err)
			continue
		}

		log.Debug("process tx match", "order", order)
		// process Matching Engine
		newTrades, newRejectedOrders, err := tomoXService.ApplyOrder(tokenDecimals, header, coinbase, v.bc, statedb, tomoxStatedb, tradingstate.GetTradingOrderBookHash(order.BaseToken, order.QuoteToken), order)
		if err != nil {
			return err
		}
		tradingResult[tradingstate.GetMatchingResultCacheKey(order)] = tradingstate.MatchingResult{
			Trades:  newTrades,
			Rejects: newRejectedOrders,
		}
	}
	if tomoXService.IsSDKNode() {
		v.bc.AddMatchingResult(txMatchBatch.TxHash, tradingResult)
	}
	return nil
}

func (v *BlockValidator) ValidateLendingOrder(tokenDecimals map[common.Address]*big.Int,statedb *state.StateDB, lendingStateDb *lendingstate.LendingStateDB, tomoxStatedb *tradingstate.TradingStateDB, batch lendingstate.TxLendingBatch, coinbase common.Address, header *types.Header) error {
	posvEngine, ok := v.bc.Engine().(*posv.Posv)
	if posvEngine == nil || !ok {
		return ErrNotPoSV
	}
	tomoXService := posvEngine.GetTomoXService()
	if tomoXService == nil {
		return fmt.Errorf("tomox not found")
	}
	lendingService := posvEngine.GetLendingService()
	if lendingService == nil {
		return fmt.Errorf("lendingService not found")
	}
	log.Debug("verify lendingItem ", "numItems", len(batch.Data))
	lendingResult := map[common.Hash]lendingstate.MatchingResult{}
	for _, l := range batch.Data {
		// verify lendingItem

		log.Debug("process lending tx", "lendingItem", lendingstate.ToJSON(l))
		// process Matching Engine
		newTrades, newRejectedOrders, err := lendingService.ApplyOrder(tokenDecimals, header, coinbase, v.bc, statedb, lendingStateDb, tomoxStatedb, lendingstate.GetLendingOrderBookHash(l.LendingToken, l.Term), l)
		if err != nil {
			return err
		}
		lendingResult[lendingstate.GetLendingCacheKey(l)] = lendingstate.MatchingResult{
			Trades:  newTrades,
			Rejects: newRejectedOrders,
		}
	}
	if tomoXService.IsSDKNode() {
		v.bc.AddLendingResult(batch.TxHash, lendingResult)
	}
	return nil
}

// CalcGasLimit computes the gas limit of the next block after parent.
// This is miner strategy, not consensus protocol.
func CalcGasLimit(parent *types.Block) uint64 {
	// contrib = (parentGasUsed * 3 / 2) / 1024
	contrib := (parent.GasUsed() + parent.GasUsed()/2) / params.GasLimitBoundDivisor

	// decay = parentGasLimit / 1024 -1
	decay := parent.GasLimit()/params.GasLimitBoundDivisor - 1

	/*
		strategy: gasLimit of block-to-mine is set based on parent's
		gasUsed value.  if parentGasUsed > parentGasLimit * (2/3) then we
		increase it, otherwise lower it (or leave it unchanged if it's right
		at that usage) the amount increased/decreased depends on how far away
		from parentGasLimit * (2/3) parentGasUsed is.
	*/
	limit := parent.GasLimit() - decay + contrib
	if limit < params.MinGasLimit {
		limit = params.MinGasLimit
	}
	// however, if we're now below the target (TargetGasLimit) we increase the
	// limit as much as we can (parentGasLimit / 1024 -1)
	if limit < params.TargetGasLimit {
		limit = parent.GasLimit() + decay
		if limit > params.TargetGasLimit {
			limit = params.TargetGasLimit
		}
	}
	return limit
}

func ExtractTradingTransactions(transactions types.Transactions) ([]tradingstate.TxMatchBatch, error) {
	txMatchBatchData := []tradingstate.TxMatchBatch{}
	for _, tx := range transactions {
		if tx.IsTradingTransaction() {
			txMatchBatch, err := tradingstate.DecodeTxMatchesBatch(tx.Data())
			if err != nil {
				log.Error("transaction match is corrupted. Failed to decode txMatchBatch", "err", err, "txHash", tx.Hash().Hex())
				continue
			}
			txMatchBatch.TxHash = tx.Hash()
			txMatchBatchData = append(txMatchBatchData, txMatchBatch)
		}
	}
	return txMatchBatchData, nil
}

func ExtractLendingTransactions(transactions types.Transactions) ([]lendingstate.TxLendingBatch, error) {
	batchData := []lendingstate.TxLendingBatch{}
	for _, tx := range transactions {
		if tx.IsLendingTransaction() {
			txMatchBatch, err := lendingstate.DecodeTxLendingBatch(tx.Data())
			if err != nil {
				log.Error("transaction match is corrupted. Failed to decode lendingTransaction", "err", err, "txHash", tx.Hash().Hex())
				continue
			}
			txMatchBatch.TxHash = tx.Hash()
			batchData = append(batchData, txMatchBatch)
		}
	}
	return batchData, nil
}

func ExtractLendingFinalizedTradeTransactions(transactions types.Transactions) (lendingstate.FinalizedResult, error) {
	for _, tx := range transactions {
		if tx.IsLendingFinalizedTradeTransaction() {
			finalizedTrades, err := lendingstate.DecodeFinalizedResult(tx.Data())
			if err != nil {
				log.Error("transaction is corrupted. Failed to decode LendingClosedTradeTransaction", "err", err, "txHash", tx.Hash().Hex())
				continue
			}
			finalizedTrades.TxHash = tx.Hash()
			// each block has only one tx of this type
			return finalizedTrades, nil
		}
	}
	return lendingstate.FinalizedResult{}, nil
}

// runContract run smart contract
func runContract(chain consensus.ChainContext, statedb *state.StateDB, contractAddr common.Address, abi *abi.ABI, method string, args ...interface{}) (interface{}, error) {
	input, err := abi.Pack(method)
	if err != nil {
		return nil, err
	}
	fakeCaller := common.HexToAddress("0x0000000000000000000000000000000000000001")
	call := tomochain.CallMsg{To: &contractAddr, Data: input, From: fakeCaller}
	call.GasPrice = big.NewInt(0)
	if call.Gas == 0 {
		call.Gas = 1000000
	}
	if call.Value == nil {
		call.Value = new(big.Int)
	}
	// Execute the call.
	msg := Callmsg{call}
	feeCapacity := state.GetTRC21FeeCapacityFromState(statedb)
	if msg.To() != nil {
		if value, ok := feeCapacity[*msg.To()]; ok {
			msg.CallMsg.BalanceTokenFee = value
		}
	}
	evmContext := NewEVMContext(msg, chain.CurrentHeader(), chain, nil)
	// Create a new environment which holds all relevant information
	// about the transaction and calling mechanisms.
	vmenv := vm.NewEVM(evmContext, statedb, nil, chain.Config(), vm.Config{})
	gaspool := new(GasPool).AddGas(1000000)
	owner := common.Address{}
	rval, _, _, err := NewStateTransition(vmenv, msg, gaspool).TransitionDb(owner)
	if err != nil {
		return nil, err
	}
	return rval, err
	var unpackResult interface{}
	err = abi.Unpack(&unpackResult, method, rval)
	if err != nil {
		return nil, err
	}
	return unpackResult, nil
}

// getTokenAbi return token abi
func getTokenAbi() (*abi.ABI, error) {
	contractABI, err := abi.JSON(strings.NewReader(contract.TRC21ABI))
	if err != nil {
		return nil, err
	}
	return &contractABI, nil
}
func getTokenDecimal(chain consensus.ChainContext, statedb *state.StateDB, tokenAddr common.Address) (tokenDecimal *big.Int, err error) {
	if tokenAddr.String() == common.TomoNativeAddress {
		return common.BasePrice, nil
	}
	var decimals uint8
	defer func() {
		log.Debug("GetTokenDecimal from ", "relayerSMC", common.RelayerRegistrationSMC, "tokenAddr", tokenAddr.Hex(), "decimals", decimals, "tokenDecimal", tokenDecimal, "err", err)
	}()
	var contractABI *abi.ABI
	contractABI, err = getTokenAbi()
	if err != nil {
		return nil, err
	}
	stateCopy := statedb.Copy()
	result, err := runContract(chain, stateCopy, tokenAddr, contractABI, "decimals")
	if err != nil {
		return nil, err
	}
	decimals = result.(uint8)

	tokenDecimal = new(big.Int).SetUint64(0).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return tokenDecimal, nil
}
func UpdateAllTokensDecimal(chain consensus.ChainContext, statedb *state.StateDB, tokenDecimals map[common.Address]*big.Int) map[common.Address]*big.Int {
	allPairs, _ := lendingstate.GetAllLendingPairs(statedb)
	allTradingTokens, _ := tradingstate.GetAllTradingTokens(statedb)
	for _, pair := range allPairs {
		if decimal := tokenDecimals[pair.LendingToken]; decimal == nil || decimal.Sign() == 0 {
			tokenDecimal, _ := getTokenDecimal(chain, statedb, pair.LendingToken)
			if tokenDecimal != nil && tokenDecimal.Sign() > 0 {
				tokenDecimals[pair.LendingToken] = tokenDecimal
			}
		}
		if decimal := tokenDecimals[pair.CollateralToken]; decimal == nil || decimal.Sign() == 0 {
			tokenDecimal, _ := getTokenDecimal(chain, statedb, pair.CollateralToken)
			if tokenDecimal != nil && tokenDecimal.Sign() > 0 {
				tokenDecimals[pair.CollateralToken] = tokenDecimal
			}
		}
	}
	for token, _ := range allTradingTokens {
		if decimal := tokenDecimals[token]; decimal == nil || decimal.Sign() == 0 {
			tokenDecimal, _ := getTokenDecimal(chain, statedb, token)
			if tokenDecimal != nil && tokenDecimal.Sign() > 0 {
				tokenDecimals[token] = tokenDecimal
			}
		}
	}
	return tokenDecimals
}
