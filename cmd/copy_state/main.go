package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/tomochain/tomochain/cmd/utils"
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/consensus/posv"
	"github.com/tomochain/tomochain/core"
	"github.com/tomochain/tomochain/core/state"
	"github.com/tomochain/tomochain/crypto"
	"github.com/tomochain/tomochain/eth"
	"github.com/tomochain/tomochain/ethdb"
	"github.com/tomochain/tomochain/log"
	"github.com/tomochain/tomochain/trie"

	"github.com/syndtr/goleveldb/leveldb/util"
	"os"
	"runtime"
	"time"
)

var (
	from    = flag.String("from", "/data/tomo/chaindata_bak", "directory to TomoChain chaindata")
	to      = flag.String("to", "/data/tomo/chaindata_copy", "directory to clean chaindata")
	length  = flag.Uint64("length", 100, "minimum length backup state trie data")
	address = flag.String("address", "/data/tomo/adress.txt", "list address in state db")

	sercureKey       = []byte("secure-key-") // preimagePrefix + hash -> preimage
	nWorker          = runtime.NumCPU() / 2
	finish           = int32(0)
	running          = true
	emptyRoot        = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	emptyState       = crypto.Keccak256Hash(nil)
	batch            ethdb.Batch
	count            = 0
	fromDB           *ethdb.LDBDatabase
	toDB             *ethdb.LDBDatabase
	err              error
	lengthBackupData = uint64(2000)
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
	tridb := trie.NewDatabase(fromDB)
	head := core.GetHeadBlockHash(fromDB)
	header := core.GetHeader(fromDB, head, core.GetBlockNumber(fromDB, head))
	number := header.Number.Uint64() + 1
	lastestRoot := common.Hash{}
	lastestRootNumber := uint64(0)
	backupRoot := common.Hash{}
	backupNumber := uint64(0)
	for number >= 1 {
		number = number - 1
		hash := core.GetCanonicalHash(fromDB, number)
		root := core.GetHeader(fromDB, hash, number).Root
		_, err = loadSnapshot(fromDB, hash)
		if err == nil {
			backupNumber = number
		}
		_, err := trie.NewSecure(root, tridb, 0)
		if err != nil {
			continue
		}
		if common.EmptyHash(lastestRoot) {
			lastestRoot = root
			lastestRootNumber = number
			if number < backupNumber {
				backupNumber = number
			}
		} else if common.EmptyHash(backupRoot) && root != lastestRoot && number < lastestRootNumber-*length {
			backupRoot = root
			if number < backupNumber {
				backupNumber = number
			}
		}
		if backupNumber > 0 && !common.EmptyHash(lastestRoot) && !common.EmptyHash(backupRoot) {
			break
		}
	}
	if lastestRootNumber-lengthBackupData < backupNumber {
		backupNumber = lastestRootNumber - lengthBackupData
	}
	fmt.Println("lastestRoot", lastestRoot.Hex(), "lastestRootNumber", lastestRootNumber, "backupRoot", backupRoot.Hex(), "backupNumber", backupNumber, "currentNumber", header.Number.Uint64())
	err = copyHeadData()
	if err != nil {
		fmt.Println("copyHeadData", err)
		return
	}
	err = copyBlockData(backupNumber)
	if err != nil {
		fmt.Println("copyBlockData", err)
		return
	}
	err = copyStateRoot(lastestRoot)
	if err != nil {
		fmt.Println("copyBlockData", err)
		return
	}
	err = copyStateRoot(backupRoot)
	if err != nil {
		fmt.Println("copyBlockData", err)
		return
	}
	fmt.Println(time.Now(), "compact")
	toDB.LDB().CompactRange(util.Range{})
	fmt.Println(time.Now(), "end")
}
func copyHeadData() error {
	fmt.Println(time.Now(), "copyHeadData")
	//headHeaderKey = []byte("LastHeader")
	hash := core.GetHeadHeaderHash(fromDB)
	core.WriteHeadHeaderHash(toDB, hash)
	//headBlockKey  = []byte("LastBlock")
	hash = core.GetHeadBlockHash(fromDB)
	core.WriteHeadBlockHash(toDB, hash)
	//headFastKey   = []byte("LastFast")
	hash = core.GetHeadFastBlockHash(fromDB)
	core.WriteHeadFastBlockHash(toDB, hash)
	//trieSyncKey   = []byte("TrieSync")
	trie := core.GetTrieSyncProgress(fromDB)
	core.WriteTrieSyncProgress(toDB, trie)
	//genesis
	genesiHash := core.GetCanonicalHash(fromDB, 0)
	genesisBlock := core.GetBlock(fromDB, genesiHash, 0)
	genesisTd := core.GetTd(fromDB, genesiHash, 0)
	core.WriteBlock(toDB, genesisBlock)
	core.WriteTd(toDB, genesiHash, 0, genesisTd)
	core.WriteCanonicalHash(toDB, genesiHash, 0)
	//configPrefix   = []byte("ethereum-config-") // config prefix for the db
	chainConfig, err := core.GetChainConfig(fromDB, genesiHash)
	if err != nil {
		return err
	}
	core.WriteChainConfig(toDB, genesiHash, chainConfig)
	return nil
}
func copyBlockData(backupNumber uint64) error {
	fmt.Println(time.Now(), "copyBlockData", "backupNumber", backupNumber)
	head := core.GetHeadBlockHash(fromDB)
	header := core.GetHeader(fromDB, head, core.GetBlockNumber(fromDB, head))
	number := header.Number.Uint64()
	for number >= backupNumber {
		hash := header.Hash()
		//bodyPrefix          = []byte("b") // bodyPrefix + num (uint64 big endian) + hash -> block body
		//blockHashPrefix     = []byte("H") // blockHashPrefix + hash -> num (uint64 big endian)
		//headerPrefix        = []byte("h") // headerPrefix + num (uint64 big endian) + hash -> header
		block := core.GetBlock(fromDB, hash, number)
		core.WriteBlock(toDB, block)
		//tdSuffix            = []byte("t") // headerPrefix + num (uint64 big endian) + hash + tdSuffix -> td
		td := core.GetTd(fromDB, hash, number)
		core.WriteTd(toDB, hash, number, td)
		//numSuffix           = []byte("n") // headerPrefix + num (uint64 big endian) + numSuffix -> hash
		hash = core.GetCanonicalHash(fromDB, number)
		core.WriteCanonicalHash(toDB, hash, number)
		snap, err := loadSnapshot(fromDB, hash)
		if err == nil {
			fmt.Println("loaded snap shot at hash", hash.Hex(), "number", number)
			err = storeSnapshot(snap, toDB)
			if err != nil {
				fmt.Println("Fail save snap shot at hash", hash.Hex(), "number", number)
			}
		}
		if number == 0 {
			break
		}
		header = core.GetHeader(fromDB, block.ParentHash(), number-1)
		number = header.Number.Uint64()

	}
	return nil
}
func copyStateRoot(root common.Hash) error {
	fromState, err := state.New(root, state.NewDatabase(fromDB))
	if err != nil {
		fmt.Println("fromState", root.Hex(), err)
		return err
	}
	if err != nil {
		fmt.Println("fromState", err)
		return err
	}
	toStateCache := state.NewDatabase(toDB)
	toState, err := state.NewEmpty(root, toStateCache)
	if err != nil {
		fmt.Println("toState", root.Hex(), err)
		return err
	}
	f, err := os.Open(*address)
	if err != nil {
		fmt.Println(err)
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		addr := common.HexToAddress(scanner.Text())
		copyStateData(fromState, toState, addr)
	}
	newRoot, err := toState.Commit(false)
	if err != nil {
		fmt.Println("To State commit err", err)
		return err
	}
	toStateCache.TrieDB().Commit(newRoot, false)
	fmt.Println(time.Now(), "from Root", root.Hex(), "to root", newRoot.Hex())
	if root != newRoot {
		return errors.New("Fail compare 2 state root")
	}
	return nil
}
func copyStateData(fromState *state.StateDB, toState *state.StateDB, addr common.Address) {
	fromObject := fromState.GetStateObjectNotCache(addr)
	if fromObject == nil || fromObject.Empty() {
		fmt.Println("from object empty", addr.Hex(), fromObject, fromState.Error())
		return
	}
	toObject := toState.NewObject(addr)
	toObject.SetNonce(fromObject.Nonce())
	toObject.SetBalance(fromObject.Balance())
	fromCode := fromObject.Code(fromState.Database())
	if fromCode != nil {
		toObject.SetCode(crypto.Keccak256Hash(fromCode), fromCode)
		fromState.ForEachStorageAndCheck(addr, func(key, value common.Hash) bool {
			toObject.SetState(toState.Database(), key, value)
			return true
		})
		toState.Commit(true)
	}
}

func loadSnapshot(db *ethdb.LDBDatabase, hash common.Hash) (*posv.Snapshot, error) {
	blob, err := db.Get(append([]byte("posv-"), hash[:]...))
	if err != nil {
		return nil, err
	}
	snap := new(posv.Snapshot)
	if err := json.Unmarshal(blob, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// store inserts the snapshot into the database.
func storeSnapshot(snap *posv.Snapshot, db *ethdb.LDBDatabase) error {
	blob, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return db.Put(append([]byte("posv-"), snap.Hash[:]...), blob)
}
