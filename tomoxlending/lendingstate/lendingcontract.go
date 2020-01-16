package lendingstate

import (
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/core/state"
	"github.com/tomochain/tomochain/tomox/tradingstate"
	"math/big"
)

var (
	LendingRelayerListSlot     = uint64(0)
	CollateralMapSlot          = uint64(1)
	DefaultCollateralSlot      = uint64(2)
	SupportedBaseSlot          = uint64(3)
	SupportedTermSlot          = uint64(4)
	SupportedAllCollateralSlot = uint64(5)
	LendingRelayerStructSlots  = map[string]*big.Int{
		"fee":         big.NewInt(0),
		"bases":       big.NewInt(1),
		"terms":       big.NewInt(2),
		"collaterals": big.NewInt(3),
	}
	CollateralStructSlots = map[string]*big.Int{
		"depositRate":     big.NewInt(0),
		"liquidationRate": big.NewInt(1),
		"price":           big.NewInt(2),
	}
)

// @function IsValidRelayer : return whether the given address is the coinbase of a valid relayer or not
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @return: true if it's a valid coinbase address of lending protocol, otherwise return false
func IsValidRelayer(statedb *state.StateDB, coinbase common.Address) bool {
	locRelayerState := GetLocMappingAtKey(coinbase.Hash(), LendingRelayerListSlot)

	if v := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), common.BytesToHash(locRelayerState.Bytes())); v != (common.Hash{}) {
		return true
	}
	return false
}

// @function GetFee
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @return: feeRate of lending
func GetFee(statedb *state.StateDB, coinbase common.Address) *big.Int {
	locRelayerState := state.GetLocMappingAtKey(coinbase.Hash(), LendingRelayerListSlot)
	locHash := common.BytesToHash(new(big.Int).Add(locRelayerState, LendingRelayerStructSlots["fee"]).Bytes())
	return statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locHash).Big()
}

// @function GetBaseList
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @return: list of base tokens
func GetBaseList(statedb *state.StateDB, coinbase common.Address) []common.Address {
	baseList := []common.Address{}
	locRelayerState := state.GetLocMappingAtKey(coinbase.Hash(), LendingRelayerListSlot)
	locBaseHash := state.GetLocOfStructElement(locRelayerState, LendingRelayerStructSlots["bases"])
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locBaseHash).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locBaseHash, i, 1)
		addr := common.BytesToAddress(statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Bytes())
		if addr != (common.Address{}) {
			baseList = append(baseList, addr)
		}
	}
	return baseList
}

// @function GetTerms
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @return: list of supported terms of the given relayer
func GetTerms(statedb *state.StateDB, coinbase common.Address) []uint64 {
	terms := []uint64{}
	locRelayerState := state.GetLocMappingAtKey(coinbase.Hash(), LendingRelayerListSlot)
	locTermHash := state.GetLocOfStructElement(locRelayerState, LendingRelayerStructSlots["terms"])
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locTermHash).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locTermHash, i, 1)
		t := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Big().Uint64()
		if t != uint64(0) {
			terms = append(terms, t)
		}
	}
	return terms
}

// @function IsValidPair
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @param baseToken: address of baseToken
// @param terms: term
// @return: TRUE if the given baseToken, term organize a valid pair
func IsValidPair(statedb *state.StateDB, coinbase common.Address, baseToken common.Address, term uint64) (valid bool, pairIndex uint64) {
	baseTokenList := GetBaseList(statedb, coinbase)
	terms := GetTerms(statedb, coinbase)
	baseIndexes := []uint64{}
	for i := uint64(0); i < uint64(len(baseTokenList)); i++ {
		if baseTokenList[i] == baseToken {
			baseIndexes = append(baseIndexes, i)
		}
	}
	for _, index := range baseIndexes {
		if terms[index] == term {
			pairIndex = index
			return true, pairIndex
		}
	}
	return false, pairIndex
}

// @function GetCollaterals
// @param statedb : current state
// @param coinbase: coinbase address of relayer
// @param baseToken: address of baseToken
// @param terms: term
// @return:
//		- collaterals []common.Address  : list of addresses of collateral
//		- isSpecialCollateral			: TRUE if collateral is a token which is NOT available for trading in TomoX, otherwise FALSE
func GetCollaterals(statedb *state.StateDB, coinbase common.Address, baseToken common.Address, term uint64) (collaterals []common.Address, isSpecialCollateral bool) {
	validPair, pairIndex := IsValidPair(statedb, coinbase, baseToken, term)
	if !validPair {
		return []common.Address{}, false
	}

	locRelayerState := state.GetLocMappingAtKey(coinbase.Hash(), LendingRelayerListSlot)
	locCollateralHash := state.GetLocOfStructElement(locRelayerState, LendingRelayerStructSlots["collaterals"])
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locCollateralHash).Big().Uint64()

	loc := state.GetLocDynamicArrAtElement(locCollateralHash, pairIndex, 1)
	collateralAddr := common.BytesToAddress(statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Bytes())
	if collateralAddr != (common.Address{}) && collateralAddr != (common.HexToAddress("0x0")) {
		return []common.Address{collateralAddr}, true
	}

	// if collaterals is not defined for the relayer, return default collaterals
	locDefaultCollateralHash := state.GetLocSimpleVariable(DefaultCollateralSlot)
	length = statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locDefaultCollateralHash).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locDefaultCollateralHash, i, 1)
		addr := common.BytesToAddress(statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Bytes())
		if addr != (common.Address{}) {
			collaterals = append(collaterals, addr)
		}
	}
	return collaterals, false
}

// @function GetCollateralDetail
// @param statedb : current state
// @param token: address of collateral token
// @return: depositRate, liquidationRate, price of collateral
func GetCollateralDetail(statedb *state.StateDB, token common.Address) (depositRate *big.Int, liquidationRate *big.Int, price *big.Int) {
	collateralState := GetLocMappingAtKey(token.Hash(), CollateralMapSlot)
	locDepositRate := state.GetLocOfStructElement(collateralState, CollateralStructSlots["depositRate"])
	locLiquidationRate := state.GetLocOfStructElement(collateralState, CollateralStructSlots["liquidationRate"])
	locCollateralPrice := state.GetLocOfStructElement(collateralState, CollateralStructSlots["price"])
	depositRate = statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locDepositRate).Big()
	liquidationRate = statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locLiquidationRate).Big()
	price = statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locCollateralPrice).Big()
	return depositRate, liquidationRate, price
}

// @function GetSupportedTerms
// @param statedb : current state
// @return: list of terms which tomoxlending supports
func GetSupportedTerms(statedb *state.StateDB) []uint64 {
	terms := []uint64{}
	locSupportedTerm := state.GetLocSimpleVariable(SupportedTermSlot)
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locSupportedTerm).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locSupportedTerm, i, 1)
		t := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Big().Uint64()
		if t != 0 {
			terms = append(terms, t)
		}
	}
	return terms
}

// @function GetAllBaseTokens
// @param statedb : current state
// @return: list of tokens which are available for lending
func GetAllBaseTokens(statedb *state.StateDB) []common.Address {
	baseTokens := []common.Address{}
	locSupportedBaseToken := state.GetLocSimpleVariable(SupportedBaseSlot)
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locSupportedBaseToken).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locSupportedBaseToken, i, 1)
		addr := common.BytesToAddress(statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Bytes())
		if addr != (common.Address{}) {
			baseTokens = append(baseTokens, addr)
		}
	}
	return baseTokens
}

func GetAllCollateralTokens(statedb *state.StateDB) []common.Address {
	collateralTokens := []common.Address{}
	locLengthHash := state.GetLocSimpleVariable(SupportedAllCollateralSlot)
	length := statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), locLengthHash).Big().Uint64()
	for i := uint64(0); i < length; i++ {
		loc := state.GetLocDynamicArrAtElement(locLengthHash, i, 1)
		addr := common.BytesToAddress(statedb.GetState(common.HexToAddress(common.LendingRegistrationSMC), loc).Bytes())
		if addr != (common.Address{}) {
			collateralTokens = append(collateralTokens, addr)
		}
	}
	return collateralTokens
}

func GetAllLendingPairs(statedb *state.StateDB) ([]LendingPair, error) {
	lendingTokens := GetAllBaseTokens(statedb)
	collateralTokens := GetAllCollateralTokens(statedb)
	allPairs := []LendingPair{}
	allTradingBook := map[common.Hash]bool{}
	for _, lendingToken := range lendingTokens {
		for _, collacteralToken := range collateralTokens {
			tradingBook := tradingstate.GetTradingOrderBookHash(collacteralToken, lendingToken)
			if exit, _ := allTradingBook[tradingBook]; !exit {
				allTradingBook[tradingBook] = true
				allPairs = append(allPairs, LendingPair{CollateralToken: collacteralToken, LendingToken: lendingToken})
			}
		}
	}
	return allPairs, nil
}

func GetAllLendingBooks(statedb *state.StateDB) (map[common.Hash]bool, error) {
	allPairs := map[common.Hash]bool{}
	lendingTokens := GetAllBaseTokens(statedb)
	terms := GetSupportedTerms(statedb)
	for _, lendingToken := range lendingTokens {
		for _, term := range terms {
			allPairs[GetLendingOrderBookHash(lendingToken, term)] = true
		}
	}
	return allPairs, nil
}
