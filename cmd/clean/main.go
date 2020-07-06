package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/tomochain/tomochain/cmd/utils"
	"github.com/tomochain/tomochain/common"
	"github.com/tomochain/tomochain/consensus/posv"
	"github.com/tomochain/tomochain/core"
	"github.com/tomochain/tomochain/core/state"
	"github.com/tomochain/tomochain/crypto"
	"github.com/tomochain/tomochain/crypto/sha3"
	"github.com/tomochain/tomochain/eth"
	"github.com/tomochain/tomochain/ethdb"
	"github.com/tomochain/tomochain/log"
	"github.com/tomochain/tomochain/rlp"
	"github.com/tomochain/tomochain/trie"
	lru "github.com/hashicorp/golang-lru"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"hash"
	"os"
	"runtime"
	"time"
)

var (
	from   = flag.String("from", "/data/tomo/chaindata_bak", "directory to TomoChain chaindata")
	to     = flag.String("to", "/data/tomo/chaindata_copy", "directory to clean chaindata")
	length = flag.Uint64("length", 100, "minimum length backup state trie data")
	addr   = flag.String("addr", "", "file contain address want copy state trie")

	nWorker          = runtime.NumCPU() / 2
	finish           = int32(0)
	running          = true
	emptyRoot        = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
	emptyState       = crypto.Keccak256Hash(nil)
	emptyCode        = crypto.Keccak256Hash(nil)
	batch            ethdb.Batch
	count            = 0
	fromDB           *ethdb.LDBDatabase
	toDB             *ethdb.LDBDatabase
	err              error
	lengthBackupData = uint64(5000)
	cacheCode, _     = lru.NewARC(10000)
)

func main() {
	fmt.Println(emptyCode.Hex())
	flag.Parse()
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
	number := header.Number.Uint64() - 50
	lastestRoot := common.Hash{}
	lastestRootNumber := uint64(0)
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
		}
		if backupNumber > 0 && !common.EmptyHash(lastestRoot) {
			break
		}
	}
	if lastestRootNumber-lengthBackupData < backupNumber {
		backupNumber = lastestRootNumber - lengthBackupData
	}
	fmt.Println("lastestRoot", lastestRoot.Hex(), "lastestRootNumber", lastestRootNumber, "backupNumber", backupNumber, "currentNumber", header.Number.Uint64())
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
	if len(*addr) > 0 {
		f, err := os.Open(*addr)
		if err != nil {
			fmt.Println(err)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			addr := common.HexToAddress(scanner.Text())
			err = copyAddressData(addr, lastestRoot)
			if err != nil {
				fmt.Println("copyState Address dataRoot", addr, lastestRoot.Hex(), "err", err)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			log.Crit("scan", "err", err)
		}
	} else {
		err = copyStateData(lastestRoot, true)
		if err != nil {
			fmt.Println("copyStateData lastestRoot", lastestRoot.Hex(), "err", err)
			return
		}
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

func copyStateData(root common.Hash, checkAddr bool) error {
	fmt.Println(time.Now(), "run copy state data ", "root", root.Hex())
	batch = toDB.NewBatch()
	rootNode, valueDB, err := resolveHash(root[:], fromDB.LDB())
	if err != nil {
		return err
	}
	err = processNode(rootNode, nil, checkAddr, false)
	if err != nil {
		return err
	}
	err = toDB.LDB().Put(root[:], valueDB, nil)
	if err != nil {
		return err
	}
	err = batch.Write()
	if err != nil {
		return err
	}
	return nil
}

func copyAddressData(addr common.Address, lastestRoot common.Hash) error {
	fmt.Println(time.Now(), "run copy address data ", addr.Hex(), "root", lastestRoot.Hex())
	batch = toDB.NewBatch()
	rootNode, valueDB, err := resolveHash(lastestRoot[:], fromDB.LDB())
	if err != nil {
		return err
	}
	path := keybytesToHex(hashKey(addr.Bytes()))
	err = findAddress(rootNode, path, 0)
	if err != nil {
		return err
	}
	err = toDB.LDB().Put(lastestRoot[:], valueDB, nil)
	if err != nil {
		return err
	}
	err = batch.Write()
	if err != nil {
		return err
	}
	return nil
}
func putToDataCopy(key []byte, value []byte) {
	count++
	batch.Put(key, value)
	if count%1000 == 0 {
		err := batch.Write()
		count = 0
		if err != nil {
			fmt.Println("Error when put data to copy db")
			panic(err)
		}
		batch.Reset()
	}
}
func findAddress(n trie.Node, path []byte, pos int) error {
	switch node := n.(type) {
	case *trie.FullNode:
		// Full Node, move to the first non-nil child.
		childNode := node.Children[path[pos]]
		var err error = nil
		var valueDB []byte
		var keyDB []byte
		if _, ok := childNode.(trie.HashNode); ok {
			keyDB = childNode.(trie.HashNode)
			childNode, valueDB, err = resolveHash(keyDB, fromDB.LDB())
		}
		if err != nil {
			fmt.Println("resolveHash", err, node, path)
			return err
		}
		err = findAddress(childNode, path, pos+1)
		if err != nil {
			return err
		}
		if keyDB != nil {
			putToDataCopy(keyDB, valueDB)
		}
	case *trie.ShortNode:
		// Short Node, return the pointer singleton child
		childNode := node.Val
		var err error = nil
		var valueDB []byte
		var keyDB []byte
		if _, ok := node.Val.(trie.HashNode); ok {
			keyDB = node.Val.(trie.HashNode)
			childNode, valueDB, err = resolveHash(keyDB, fromDB.LDB())
		}
		if err != nil {
			fmt.Println("resolveHash", err, node, path)
			return err
		}
		err = findAddress(childNode, path, pos+len(node.Key))
		if err != nil {
			return err
		}
		if keyDB != nil {
			putToDataCopy(keyDB, valueDB)
		}
	case trie.ValueNode:

		//keyDB := append(sercureKey, hexToKeybytes(path)...)
		//valueDB, err := fromDB.Get(keyDB)
		//if err != nil {
		//	fmt.Println("Not found key ", common.Bytes2Hex(keyDB))
		//	return err
		//}
		//key := common.Bytes2Hex(valueDB)
		//fmt.Println("find key ", key, "path", common.Bytes2Hex(path), " => ", common.Bytes2Hex(keybytesToHex(hashKey(valueDB))))
		//putToDataCopy(keyDB, valueDB)

		var data state.Account
		if err := rlp.DecodeBytes(node, &data); err != nil {
			fmt.Println("Failed to decode state object", "path", common.Bytes2Hex(path), "value", common.Bytes2Hex(node))
			return err
		}
		if !common.EmptyHash(data.Root) && data.Root != emptyRoot && data.Root != emptyState {
			newNode, valueDB, err := resolveHash(data.Root[:], fromDB.LDB())
			if err != nil {
				return err
			}
			err = processNode(newNode, nil, false, true)
			if err != nil {
				return err
			}
			putToDataCopy(data.Root[:], valueDB)
		}
	default:
		fmt.Println("invalid Node", node, common.Bytes2Hex(path))
	}
	return nil
}
func processNode(n trie.Node, path []byte, checkAddr bool, log bool) error {
	switch node := n.(type) {
	case *trie.FullNode:
		// Full Node, move to the first non-nil child.
		for i := 0; i < len(node.Children); i++ {
			child := node.Children[i]
			if child != nil {
				childNode := child
				var err error = nil
				var valueDB []byte
				var keyDB []byte
				if _, ok := child.(trie.HashNode); ok {
					keyDB = child.(trie.HashNode)
					childNode, valueDB, err = resolveHash(keyDB, fromDB.LDB())
				}
				if err != nil {
					fmt.Println("resolveHash", err, node, path, checkAddr)
					return err
				}
				err = processNode(childNode, append(path, byte(i)), checkAddr, log)
				if err != nil {
					return err
				}
				if keyDB != nil {
					putToDataCopy(keyDB, valueDB)
				}
			}
		}
	case *trie.ShortNode:
		// Short Node, return the pointer singleton child
		childNode := node.Val
		var err error = nil
		var valueDB []byte
		var keyDB []byte
		if _, ok := node.Val.(trie.HashNode); ok {
			keyDB = node.Val.(trie.HashNode)
			childNode, valueDB, err = resolveHash(keyDB, fromDB.LDB())
		}
		if err != nil {
			fmt.Println("resolveHash", err, node, path, checkAddr)
			return err
		}
		err = processNode(childNode, append(path, node.Key...), checkAddr, log)
		if err != nil {
			return err
		}
		if keyDB != nil {
			putToDataCopy(keyDB, valueDB)
		}
	case trie.ValueNode:
		if checkAddr {
			var data state.Account
			if err := rlp.DecodeBytes(node, &data); err != nil {
				fmt.Println("Failed to decode state object", "path", common.Bytes2Hex(path), "value", common.Bytes2Hex(node))
				return err
			}
			if !common.EmptyHash(data.Root) && data.Root != emptyRoot && data.Root != emptyState {
				newNode, valueDB, err := resolveHash(data.Root[:], fromDB.LDB())
				if err != nil {
					return err
				}
				err = processNode(newNode, nil, false, false)
				if err != nil {
					return err
				}
				putToDataCopy(data.Root[:], valueDB)
			}
			codeHash := common.BytesToHash(data.CodeHash)
			if codeHash != emptyCode && !cacheCode.Contains(codeHash) {
				enc, err := fromDB.Get(data.CodeHash)
				if err != nil {
					return err
				}
				putToDataCopy(data.CodeHash, enc)
				cacheCode.Add(codeHash, true)
				fmt.Println("copy code hash", codeHash.Hex())
			}
		}
	default:
		fmt.Println("invalid Node", node, common.Bytes2Hex(path), checkAddr)
	}
	return nil
}

func resolveHash(n trie.HashNode, db *leveldb.DB) (trie.Node, []byte, error) {
	enc, err := db.Get(n, nil)
	if err != nil || enc == nil {
		return nil, nil, &trie.MissingNodeError{}
	}
	return trie.MustDecodeNode(n, enc, 0), enc, nil
}

func hexToKeybytes(hex []byte) []byte {
	if hasTerm(hex) {
		hex = hex[:len(hex)-1]
	}
	if len(hex)&1 != 0 {
		panic("can't convert hex key of odd length")
	}
	key := make([]byte, (len(hex)+1)/2)
	decodeNibbles(hex, key)
	return key
}

// hasTerm returns whether a hex key has the terminator flag.
func hasTerm(s []byte) bool {
	return len(s) > 0 && s[len(s)-1] == 16
}

func decodeNibbles(nibbles []byte, bytes []byte) {
	for bi, ni := 0, 0; ni < len(nibbles); bi, ni = bi+1, ni+2 {
		bytes[bi] = nibbles[ni]<<4 | nibbles[ni+1]
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

type hasher struct {
	tmp        *bytes.Buffer
	sha        hash.Hash
	cachegen   uint16
	cachelimit uint16
}

func newHasher(cachegen, cachelimit uint16) *hasher {
	h := &hasher{tmp: new(bytes.Buffer), sha: sha3.NewKeccak256()}
	h.cachegen, h.cachelimit = cachegen, cachelimit
	return h
}

func hashKey(key []byte) []byte {
	h := newHasher(0, 0)
	h.sha.Reset()
	h.sha.Write(key)
	buf := h.sha.Sum([]byte{})
	return buf
}

func keybytesToHex(str []byte) []byte {
	l := len(str)*2 + 1
	var nibbles = make([]byte, l)
	for i, b := range str {
		nibbles[i*2] = b / 16
		nibbles[i*2+1] = b % 16
	}
	nibbles[l-1] = 16
	return nibbles
}
