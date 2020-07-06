package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/crypto"
	"github.com/tomochain/tomochain/eth"
	"github.com/tomochain/tomochain/ethdb"
	"github.com/tomochain/tomochain/log"
	"github.com/tomochain/tomochain/rlp"
	"github.com/tomochain/tomochain/cmd/utils"
	"github.com/tomochain/tomochain/core/state"
	lru "github.com/hashicorp/golang-lru"
	"github.com/syndtr/goleveldb/leveldb"
	"os"
	"runtime"
)

var (
	from    = flag.String("from", "/data/tomo/chaindata", "directory to TomoChain chaindata")
	to      = flag.String("to", "/data/tomo/copy", "directory to clean chaindata")
	root    = flag.String("root", "0xc96e205f8e0d7dccea94c04fde6e6f7e508a3bbb91d2630335b37fbe23ec3a87", "state root compare")
	address = flag.String("address", "/data/tomo/adress.txt", "list address in state db")

	sercureKey   = []byte("secure-key-") // preimagePrefix + hash -> preimage
	nWorker      = runtime.NumCPU() / 2
	finish       = int32(0)
	running      = true
	emptyRoot    = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	emptyState   = crypto.Keccak256Hash(nil).Bytes()
	batch        ethdb.Batch
	count        = 0
	fromDB       *ethdb.LDBDatabase
	toDB         *ethdb.LDBDatabase
	err          error
	emptyCode    = crypto.Keccak256Hash(nil)
	cacheCode, _ = lru.NewARC(10000)
)

func main() {
	flag.Parse()
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlError, log.StreamHandler(os.Stdout, log.TerminalFormat(true))))
	fromDB, err = ethdb.NewLDBDatabase(*from, eth.DefaultConfig.DatabaseCache, utils.MakeDatabaseHandles())
	defer fromDB.Close()
	if err != nil {
		fmt.Println("fromDB", err)
		return
	}
	toDB, err = ethdb.NewLDBDatabase(*to, eth.DefaultConfig.DatabaseCache, utils.MakeDatabaseHandles())
	defer toDB.Close()
	if err != nil {
		fmt.Println("toDB", err)
		return
	}
	fromState, err := state.New(common.HexToHash(*root), state.NewDatabase(fromDB))
	if err != nil {
		fmt.Println("fromState", *root, err)
		return
	}
	toState, err := state.New(common.HexToHash(*root), state.NewDatabase(toDB))
	if err != nil {
		fmt.Println("toState", err)
		return
	}
	f, err := os.Open(*address)
	if err != nil {
		fmt.Println(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		addr := common.HexToAddress(scanner.Text())
		if !checkAddress(addr, fromState, toState, toDB.LDB()) {
			fmt.Println(addr.Hex())
		}
	}
	if err := scanner.Err(); err != nil {
		log.Crit("scan", "err", err)
	}
}
func checkAddress(addr common.Address, fromState *state.StateDB, toState *state.StateDB, toDB *leveldb.DB) bool {
	objectFrom := fromState.GetStateObjectNotCache(addr)
	if objectFrom == nil {
		return true
	}
	byteFrom, err := rlp.EncodeToBytes(objectFrom)
	if err != nil {
		return true
	}
	objectTo := toState.GetStateObjectNotCache(addr)
	if objectTo == nil {
		return false
	}
	byteTo, err := rlp.EncodeToBytes(objectTo)
	if err != nil {
		return false
	}

	if bytes.Compare(byteFrom, byteTo) != 0 {
		return false
	}
	codeHash := common.BytesToHash(objectTo.CodeHash())
	if !common.EmptyHash(codeHash) && emptyCode != codeHash && !cacheCode.Contains(codeHash) {
		code, err := toDB.Get(codeHash[:], nil)
		if err != nil || len(code) == 0 {
			return false
		}
		cacheCode.Add(cacheCode, true)
	}
	check := fromState.ForEachStorageAndCheck(addr, func(key, value common.Hash) bool {
		value = objectFrom.GetStateNotCache(fromState.Database(), key)
		toValue := objectTo.GetStateNotCache(toState.Database(), key)
		if bytes.Compare(toValue.Bytes(), value.Bytes()) != 0 {
			return false
		}
		return true
	})
	return check
}
