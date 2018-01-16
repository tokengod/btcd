package superblock

import (
	"fmt"
	"sync"
	"time"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
)

const (
	SuperBlockWork = 0x207fffff
)

var FoundationAddresses = [blockchain.SuperBlockCount]string{
	"1FRPx6jVcEQYwPZWkacUpghscrJuYq49Fy",
	"15Cbf6GGEUF4LmyTPc2XTxMvBmn9d2ADtH",
	"15s5aGvaYWdwFbY1CWdy9Zs5tvVesVnFbq",
}

var FoundationRewards = [blockchain.SuperBlockCount]int64{
	200000 * 100000000,
	5000 * 100000000,
	5000 * 100000000,
}

type SuperBlockGenerator struct {
	chain      *blockchain.BlockChain
	timeSource blockchain.MedianTimeSource
	workDone   bool
	lock       sync.Mutex
}

func New(chain *blockchain.BlockChain, timeSource blockchain.MedianTimeSource) *SuperBlockGenerator {
	generator := &SuperBlockGenerator{
		chain:      chain,
		timeSource: timeSource,
	}
	chain.Subscribe(generator.handleBlockchainNotification)
	return generator
}

func (g *SuperBlockGenerator) handleBlockchainNotification(notification *blockchain.Notification) {
	if g.workDone {
		return
	}

	if notification.Type == blockchain.NTBlockAccepted {
		block, ok := notification.Data.(*btcutil.Block)
		if ok == false {
			return
		}

		h := block.Height()
		fmt.Printf("--> handle new block with height %v\n", h)
		if h >= blockchain.LastPowBlockHeight+blockchain.SuperBlockCount {
			g.workDone = true
			return
		}

		if h < blockchain.LastPowBlockHeight {
			return
		}

		go g.insertSuperBlock(h - blockchain.LastPowBlockHeight)
	}
}

func (g *SuperBlockGenerator) insertSuperBlock(i int32) {
	g.lock.Lock()
	defer g.lock.Unlock()

	block, err := g.generateSuperBlock(i)
	if err != nil {
		panic(err)
	}

	_, isOrphan, err := g.chain.ProcessBlock(block, blockchain.BFNone)
	if err != nil {
		panic("insert super block failed:" + err.Error())
	} else if isOrphan {
		panic("super block shouldn't be orphan")
	}

	log.Info("Insert superblock succeed")
}

func (g *SuperBlockGenerator) generateSuperBlock(i int32) (*btcutil.Block, error) {
	fmt.Printf("---> generate super block %v\n", i)

	best := g.chain.BestSnapshot()
	nextBlockHeight := best.Height + 1

	extraNonce := uint64(0)
	coinbaseScript, err := txscript.NewScriptBuilder().AddInt64(int64(nextBlockHeight)).
		AddInt64(int64(extraNonce)).AddData([]byte(mining.CoinbaseFlags)).
		Script()
	if err != nil {
		return nil, err
	}

	payToAddress, err := btcutil.DecodeAddress(FoundationAddresses[i], &chaincfg.MainNetParams)
	if err != nil {
		panic(err)
	}
	pkScript, err := txscript.PayToAddrScript(payToAddress)
	if err != nil {
		return nil, err
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{
		// no input
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{},
			wire.MaxPrevOutIndex),
		SignatureScript: coinbaseScript,
		Sequence:        wire.MaxTxInSequenceNum,
	})
	tx.AddTxOut(&wire.TxOut{
		Value:    FoundationRewards[i],
		PkScript: pkScript,
	})
	coinbaseTx := btcutil.NewTx(tx)
	blockTxns := []*btcutil.Tx{coinbaseTx}

	ts := medianAdjustedTime(best, g.timeSource)
	// Calculate the next expected block version based on the state of the
	// rule change deployments.
	nextBlockVersion, err := g.chain.CalcNextBlockVersion()
	if err != nil {
		return nil, err
	}

	// Create a new block ready to be solved.
	merkles := blockchain.BuildMerkleTreeStore(blockTxns, false)
	var msgBlock wire.MsgBlock

	msgBlock.Header = wire.BlockHeader{
		Version:    nextBlockVersion,
		PrevBlock:  best.Hash,
		MerkleRoot: *merkles[len(merkles)-1],
		Timestamp:  ts,
		Bits:       SuperBlockWork,
	}
	for _, tx := range blockTxns {
		if err := msgBlock.AddTransaction(tx.MsgTx()); err != nil {
			return nil, err
		}
	}

	block := btcutil.NewBlock(&msgBlock)
	block.SetHeight(nextBlockHeight)
	if err := g.chain.CheckConnectBlockTemplate(block); err != nil {
		return nil, err
	} else {
		return block, nil
	}
}

func medianAdjustedTime(chainState *blockchain.BestState, timeSource blockchain.MedianTimeSource) time.Time {
	newTimestamp := timeSource.AdjustedTime()
	minTimestamp := chainState.MedianTime.Add(time.Second)
	if newTimestamp.Before(minTimestamp) {
		newTimestamp = minTimestamp
	}

	return newTimestamp
}
