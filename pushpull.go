package main

import (
	"fmt"
	"strconv"

	"github.com/btcsuite/btcd/btcec"
	"github.com/lightningnetwork/lnd/uspv"
)

// Do math, see if this curve thing works.
func Math(args []string) error {
	priv := SCon.TS.GetFundPrivkey(5, 5)

	pubArr := SCon.TS.GetFundPubkey(5, 5)

	pub, _ := btcec.ParsePubKey(pubArr[:], btcec.S256())
	fmt.Printf("initial  pub: %x\n", pubArr)

	for i := 0; i < 10000; i++ {
		uspv.PubKeyAddBytes(pub, []byte("bigint"))
	}
	fmt.Printf("modified pub: %x\n", pub.SerializeCompressed())

	//	for i := 0; i < 10000; i++ {
	uspv.PrivKeyAddBytes(priv, []byte("bigint"))
	//	}
	fmt.Printf("from prv pub: %x\n", priv.PubKey().SerializeCompressed())

	return nil
}

// BreakChannel closes the channel without the other party's involvement.
// The user causing the channel Break has to wait for the OP_CSV timeout
// before funds can be recovered.  Break output addresses are already in the
// DB so you can't specify anything other than which channel to break.
func BreakChannel(args []string) error {
	// need args, fail
	if len(args) < 2 {
		return fmt.Errorf("need args: break peerIdx chanIdx")
	}

	peerIdx, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil {
		return err
	}
	cIdx, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil {
		return err
	}

	qc, err := SCon.TS.GetQchanByIdx(uint32(peerIdx), uint32(cIdx))

	fmt.Printf("%s (%d,%d) h: %d a: %d\n",
		qc.Op.String(), qc.PeerIdx, qc.KeyIdx, qc.AtHeight, qc.Value)

	//	qc.NextState = new(uspv.StatCom)
	//	qc.NextState.MyAmt = 1000000
	//	qc.NextState.TheirRevHash = uspv.Hash88
	//	qc.NextState.MyRevHash = uspv.Hash88

	//	sig, err := SCon.TS.SignNextState(qc)
	//	if err != nil {
	//		return err
	//	}
	//	fmt.Printf("made sig: %x\n", sig)

	return nil
}

func PushChannel(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("need args: push peerIdx chanIdx amt")
	}
	if RemoteCon == nil {
		return fmt.Errorf("Not connected to anyone, can't push\n")
	}
	// this stuff is all the same as in cclose, should put into a function...
	peerIdx64, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil {
		return err
	}
	cIdx64, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil {
		return err
	}
	amt, err := strconv.ParseInt(args[2], 10, 32)
	if err != nil {
		return err
	}
	if amt > 100000000 || amt < 1 {
		return fmt.Errorf("push %d, max push is 1 coin / 100000000", amt)
	}
	peerIdx := uint32(peerIdx64)
	cIdx := uint32(cIdx64)
	// find the peer index of who we're connected to
	currentPeerIdx, err := SCon.TS.GetPeerIdx(RemoteCon.RemotePub)
	if err != nil {
		return err
	}
	if uint32(peerIdx) != currentPeerIdx {
		return fmt.Errorf("Want to close with peer %d but connected to %d",
			peerIdx, currentPeerIdx)
	}
	fmt.Printf("push %d to (%d,%d)\n", peerIdx, cIdx, amt)

	qc, err := SCon.TS.GetQchanByIdx(peerIdx, cIdx)
	if err != nil {
		return err
	}
	// local sanity check
	//	if amt >= qc.State.MyAmt {
	//		return fmt.Errorf("push %d, you have %d in channel", amt, qc.State.MyAmt)
	//	}

	qc.State.Delta = int32(-amt)
	// save to db with ONLY delta changed
	err = SCon.TS.SaveQchanState(qc)
	if err != nil {
		return err
	}
	qc.State.StateIdx++
	theirHAKDpub, err := qc.MakeTheirHAKDPubkey()
	if err != nil {
		return err
	}

	fmt.Printf("will send RTS with delta:%d HAKD %x\n",
		qc.State.Delta, theirHAKDpub[:4])

	// RTS is op (36), delta (4), HAKDPub (33)
	// total length 73
	// could put index as well here but for now index just goes ++ each time.

	msg := []byte{uspv.MSGID_RTS}
	msg = append(msg, uspv.OutPointToBytes(qc.Op)...)
	msg = append(msg, uspv.U32tB(uint32(amt))...)
	msg = append(msg, theirHAKDpub[:]...)
	_, err = RemoteCon.Write(msg)
	if err != nil {
		return err
	}
	// clear their HAKDpub once sent.
	return nil
}

// RTSHandler takes in an RTS and responds with an ACKSIG (if everything goes OK)
func RTSHandler(from [16]byte, RTSBytes []byte) {

	if len(RTSBytes) < 73 || len(RTSBytes) > 73 {
		fmt.Printf("got %d byte RTS, expect 73", len(RTSBytes))
		return
	}

	var opArr [36]byte
	var RTSDelta uint32
	var RTSHAKDpub [33]byte

	// deserialize RTS
	copy(opArr[:], RTSBytes[:36])
	RTSDelta = uspv.BtU32(RTSBytes[36:40])
	copy(RTSHAKDpub[:], RTSBytes[40:])

	// make sure the HAKD pubkey is a pubkey
	_, err := btcec.ParsePubKey(RTSHAKDpub[:], btcec.S256())
	if err != nil {
		fmt.Printf("RTSHandler err %s", err.Error())
		return
	}

	// find who we're talkikng to
	peerBytes := RemoteCon.RemotePub.SerializeCompressed()
	// load qchan & state from DB
	qc, err := SCon.TS.GetQchan(peerBytes, opArr)
	if err != nil {
		fmt.Printf("RTSHandler err %s", err.Error())
		return
	}
	if RTSDelta < 1 {
		fmt.Printf("RTSHandler err: RTS delta %d", RTSDelta)
		return
	}
	if int64(RTSDelta) > qc.Value-qc.State.MyAmt {
		fmt.Printf("RTSHandler err: RTS delta %d but they have %d",
			RTSDelta, qc.Value-qc.State.MyAmt)
		return
	}
	qc.State.Delta = int32(RTSDelta) // assign delta
	qc.State.MyHAKDPub = RTSHAKDpub  // assign HAKD pub
	// save delta, HAKDpub to db
	err = SCon.TS.SaveQchanState(qc)
	if err != nil {
		fmt.Printf("RTSHandler err %s", err.Error())
		return
	}
	// saved to db, now proceed to create & sign their tx, and generate their
	// HAKD pub for them to sign
	qc.State.StateIdx++
	qc.State.MyAmt += int64(qc.State.Delta)
	qc.State.Delta = 0
	sig, err := SCon.TS.SignState(qc)
	if err != nil {
		fmt.Printf("RTSHandler err %s", err.Error())
		return
	}
	fmt.Printf("made sig %x\n", sig)
	theirHAKDpub, err := qc.MakeTheirHAKDPubkey()
	if err != nil {
		fmt.Printf("RTSHandler err %s", err.Error())
		return
	}

	// ACKSIG is op (36), HAKDPub (33), sig (~70)
	// total length ~139
	msg := []byte{uspv.MSGID_ACKSIG}
	msg = append(msg, uspv.OutPointToBytes(qc.Op)...)
	msg = append(msg, theirHAKDpub[:]...)
	msg = append(msg, sig...)
	_, err = RemoteCon.Write(msg)
	return
}

// ACKSIGHandler takes in an ACKSIG and responds with an SIGREV (if everything goes OK)
func ACKSIGHandler(from [16]byte, ACKSIGBytes []byte) {

	if len(ACKSIGBytes) < 135 || len(ACKSIGBytes) > 145 {
		fmt.Printf("got %d byte RTS, expect 139", len(ACKSIGBytes))
		return
	}

	var opArr [36]byte
	var ACKSIGHAKDpub [33]byte

	// deserialize ACKSIG
	copy(opArr[:], ACKSIGBytes[:36])
	copy(ACKSIGHAKDpub[:], ACKSIGBytes[36:69])
	sig := ACKSIGBytes[69:]
	// make sure the HAKD pubkey is a pubkey
	_, err := btcec.ParsePubKey(ACKSIGHAKDpub[:], btcec.S256())
	if err != nil {
		fmt.Printf("ACKSIGHandler err %s", err.Error())
		return
	}
	// find who we're talkikng to
	peerBytes := RemoteCon.RemotePub.SerializeCompressed()
	// load qchan & state from DB
	qc, err := SCon.TS.GetQchan(peerBytes, opArr)
	if err != nil {
		fmt.Printf("ACKSIGHandler err %s", err.Error())
		return
	}

	qc.State.StateIdx++
	// construct tx and verify signature
	qc.State.TheirHAKDPub, err = qc.MakeTheirHAKDPubkey()
	if err != nil {
		fmt.Printf("ACKSIGHandler err %s", err.Error())
		return
	}

	qc.State.MyAmt += int64(qc.State.Delta) // delta should be negative
	qc.State.Delta = 0
	err = qc.VerifySig(sig)
	if err != nil {
		fmt.Printf("ACKSIGHandler err %s", err.Error())
		return
	}

	return
}

// PushChannel pushes money to the other side of the channel.  It
// creates a sigpush message and sends that to the peer
func PushSig(peerIdx, cIdx uint32, amt int64) error {
	if RemoteCon == nil {
		return fmt.Errorf("Not connected to anyone, can't push\n")
	}

	fmt.Printf("push %d to (%d,%d)\n", peerIdx, cIdx, amt)

	return nil
}

//func PullSig(from [16]byte, sigpushBytes []byte) {

//	return
//}