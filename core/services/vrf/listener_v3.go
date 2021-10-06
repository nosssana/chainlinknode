package vrf

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/pkg/errors"
	"github.com/smartcontractkit/chainlink/core/gracefulpanic"
	"github.com/smartcontractkit/chainlink/core/internal/gethwrappers/generated/vrf_coordinator_v2"
	"github.com/smartcontractkit/chainlink/core/logger"
	"github.com/smartcontractkit/chainlink/core/null"
	"github.com/smartcontractkit/chainlink/core/services/bulletprooftxmanager"
	"github.com/smartcontractkit/chainlink/core/services/eth"
	"github.com/smartcontractkit/chainlink/core/services/job"
	"github.com/smartcontractkit/chainlink/core/services/keystore"
	"github.com/smartcontractkit/chainlink/core/services/log"
	"github.com/smartcontractkit/chainlink/core/services/pipeline"
	"github.com/smartcontractkit/chainlink/core/services/postgres"
	"github.com/smartcontractkit/chainlink/core/utils"
	"github.com/theodesp/go-heaps/pairing"
	"gorm.io/gorm"
)

var (
	_ log.Listener = &listenerV3{}
	_ job.Service  = &listenerV3{}
)

type listenerV3 struct {
	utils.StartStopOnce
	cfg            Config
	l              logger.Logger
	abi            abi.ABI
	ethClient      eth.Client
	logBroadcaster log.Broadcaster
	txm            bulletprooftxmanager.TxManager
	coordinator    *vrf_coordinator_v2.VRFCoordinatorV2
	pipelineRunner pipeline.Runner
	pipelineORM    pipeline.ORM
	vorm           keystore.VRFORM
	job            job.Job
	db             *gorm.DB
	vrfks          keystore.VRF
	gethks         keystore.Eth
	reqLogs        *utils.Mailbox
	chStop         chan struct{}
	waitOnStop     chan struct{}
	// We can keep these pending logs in memory because we
	// only mark them confirmed once we send a corresponding fulfillment transaction.
	// So on node restart in the middle of processing, the lb will resend them.
	reqsMu   sync.Mutex // Both goroutines write to reqs
	reqs     []pendingRequest
	reqAdded func() // A simple debug helper

	// Data structures for reorg attack protection
	// We want a map so we can do an O(1) count update every fulfillment log we get.
	respCountMu sync.Mutex
	respCount   map[string]uint64
	// This auxiliary heap is to used when we need to purge the
	// respCount map - we repeatedly want remove the minimum log.
	// You could use a sorted list if the completed logs arrive in order, but they may not.
	blockNumberToReqID *pairing.PairHeap
}

func (lsn *listenerV3) Start() error {
	return lsn.StartOnce("VRFListenerV2", func() error {
		// Take the larger of the global vs specific.
		// Note that the v2 vrf requests specify their own confirmation requirements.
		// We wait for max(minConfs, request required confs) to be safe.
		minConfs := lsn.cfg.MinIncomingConfirmations()
		if lsn.job.VRFSpec.Confirmations > lsn.cfg.MinIncomingConfirmations() {
			minConfs = lsn.job.VRFSpec.Confirmations
		}
		unsubscribeLogs := lsn.logBroadcaster.Register(lsn, log.ListenerOpts{
			Contract: lsn.coordinator.Address(),
			ParseLog: lsn.coordinator.ParseLog,
			LogsWithTopics: map[common.Hash][][]log.Topic{
				vrf_coordinator_v2.VRFCoordinatorV2RandomWordsRequested{}.Topic(): {
					{
						log.Topic(lsn.job.VRFSpec.PublicKey.MustHash()),
					},
				},
			},
			// Do not specify min confirmations, as it varies from request to request.
		})

		// Log listener gathers request logs
		go gracefulpanic.WrapRecover(func() {
			lsn.runLogListener([]func(){unsubscribeLogs}, minConfs)
		})
		// Request handler periodically computes a set of logs which can be fulfilled.
		go gracefulpanic.WrapRecover(func() {
			lsn.runRequestHandler()
		})
		return nil
	})
}

// Returns all the confirmed logs from
// the pending queue by subscription
func (lsn *listenerV3) getConfirmedLogsBySub(latestHead uint64) map[uint64][]pendingRequest {
	lsn.reqsMu.Lock()
	defer lsn.reqsMu.Unlock()
	var toProcess = make(map[uint64][]pendingRequest)
	for i := 0; i < len(lsn.reqs); i++ {
		if lsn.reqs[i].confirmedAtBlock <= latestHead {
			toProcess[lsn.reqs[i].req.SubId] = append(toProcess[lsn.reqs[i].req.SubId], lsn.reqs[i])
		}
	}
	return toProcess
}

// TODO: on second thought, I think it is more efficient to use the HB
func (lsn *listenerV3) getLatestHead() uint64 {
	latestHead, err := lsn.ethClient.HeaderByNumber(context.Background(), nil)
	if err != nil {
		logger.Errorw("VRFListenerV2: unable to read latest head", "err", err)
		return 0
	}
	return latestHead.Number.Uint64()
}

// Remove all entries 10000 blocks or older
// to avoid a memory leak.
func (lsn *listenerV3) pruneConfirmedRequestCounts() {
	lsn.respCountMu.Lock()
	defer lsn.respCountMu.Unlock()
	min := lsn.blockNumberToReqID.FindMin()
	for min != nil {
		m := min.(fulfilledReqV2)
		if m.blockNumber > (lsn.getLatestHead() - 10000) {
			break
		}
		delete(lsn.respCount, m.reqID)
		lsn.blockNumberToReqID.DeleteMin()
		min = lsn.blockNumberToReqID.FindMin()
	}
}

// Determine a set of logs that are confirmed
// and the subscription has sufficient balance to fulfill,
// given a eth call with the max gas price.
// Note we have to consider the pending reqs already in the bptxm as already "spent" link,
// using a max link consumed in their metadata.
// A user will need a minBalance capable of fulfilling a single req at the max gas price or nothing will happen.
// This is acceptable as users can choose different keyhashes which have different max gas prices.
// Other variables which can change the bill amount between our eth call simulation and tx execution:
// - Link/eth price fluctation
// - Falling back to BHS
// However the likelihood is vanishingly small as
// 1) the window between simulation and tx execution is tiny.
// 2) the max gas price provides a very large buffer most of the time.
// Its easier to optimistically assume it will go though and in the rare case of a reversion
// we simply retry TODO: follow up where if we see a fulfillment revert, return log to the queue.
func (lsn *listenerV3) processPendingVRFRequests() {
	latestHead, err := lsn.ethClient.HeaderByNumber(context.Background(), nil)
	if err != nil {
		logger.Errorw("VRFListenerV2: unable to read latest head", "err", err)
		return
	}
	confirmed := lsn.getConfirmedLogsBySub(latestHead.Number.Uint64())
	// TODO: also probably want to order these by request time so we service oldest first
	// Get subscription balance. Note that outside of this request handler, this can only decrease while there
	// are no pending requests
	if len(confirmed) == 0 {
		logger.Infow("VRFListenerV2: no pending requests")
		return
	}
	for subID, reqs := range confirmed {
		sub, err := lsn.coordinator.GetSubscription(nil, subID)
		if err != nil {
			logger.Errorw("VRFListenerV2: unable to read latest head", "err", err)
			return
		}
		keys, err := lsn.gethks.SendingKeys()
		if err != nil {
			logger.Errorw("VRFListenerV2: unable to read latest head", "err", err)
			continue
		}
		fromAddress := keys[0].Address
		if lsn.job.VRFSpec.FromAddress != nil {
			fromAddress = *lsn.job.VRFSpec.FromAddress
		}
		maxGasPrice := lsn.cfg.KeySpecificMaxGasPriceWei(fromAddress.Address())
		startBalance := sub.Balance
		lsn.processRequestsPerSub(fromAddress.Address(), startBalance, maxGasPrice, reqs)
	}
	lsn.pruneConfirmedRequestCounts()
}

// TODO: Unit test this
func MaybeSubtractReservedLink(l logger.Logger, db *gorm.DB, fromAddress common.Address, startBalance *big.Int) (*big.Int, error) {
	var reservedLink string
	err := db.Raw(`SELECT SUM(CAST(meta->>'MaxLink' AS NUMERIC(78, 0))) 
					FROM eth_txes
					WHERE meta->>'MaxLink' IS NOT NULL
					AND (state <> 'fatal_error' AND state <> 'confirmed' AND state <> 'confirmed_missing_receipt') 
					GROUP BY from_address = ?`, fromAddress).Scan(&reservedLink).Error
	if err != nil {
		l.Errorw("VRFListenerV2", "err", err)
		return startBalance, err
	}

	if reservedLink != "" {
		reservedLinkInt, success := big.NewInt(0).SetString(reservedLink, 10)
		if !success {
			l.Errorw("VRFListenerV2: error converting reserved link", "reservedLink", reservedLink)
			return startBalance, errors.New("unable to convert returned link")
		}
		// Subtract the reserved link
		return startBalance.Sub(startBalance, reservedLinkInt), nil
	}
	return startBalance, nil
}

func (lsn *listenerV3) processRequestsPerSub(fromAddress common.Address, startBalance *big.Int, maxGasPrice *big.Int, reqs []pendingRequest) {
	startBalance, err1 := MaybeSubtractReservedLink(lsn.l, lsn.db, fromAddress, startBalance)
	if err1 != nil {
		return
	}
	// Attempt to process every request, break if we run out of balance
	var processed = make(map[string]struct{})
	for _, req := range reqs {
		// This check to see if the log was consumed needs to be in the same
		// goroutine as the mark consumed to avoid processing duplicates.
		if !lsn.shouldProcessLog(req.lb) {
			return
		}
		// Check if the vrf req has already been fulfilled
		// If so we just mark it completed
		callback, err := lsn.coordinator.GetCommitment(nil, req.req.RequestId)
		if err != nil {
			lsn.l.Errorw("VRFListenerV2: unable to check if already fulfilled, processing anyways", "err", err, "txHash", req.req.Raw.TxHash)
		} else if utils.IsEmpty(callback[:]) {
			// If seedAndBlockNumber is zero then the response has been fulfilled
			// and we should skip it
			lsn.l.Infow("VRFListenerV2: request already fulfilled", "txHash", req.req.Raw.TxHash, "subID", req.req.SubId, "callback", callback)
			lsn.markLogAsConsumed(req.lb)
			processed[req.req.RequestId.String()] = struct{}{}
			continue
		}
		bi, run, payload, gaslimit, err := lsn.getMaxLinkForFulfillment(maxGasPrice, req)
		if err != nil {
			continue
		}
		if startBalance.Cmp(bi) < 0 {
			// Insufficient funds, have to wait for a user top up
			// leave it unprocessed for now
			lsn.l.Infow("VRFListenerV2: insufficient link balance to fulfill a request, breaking", "balance", startBalance, "maxLink", bi)
			break
		}
		lsn.l.Infow("VRFListenerV2: enqueuing fulfillment", "balance", startBalance, "reqID", req.req.RequestId)
		// We have enough balance to service it, lets enqueue for bptxm
		err = postgres.NewGormTransactionManager(lsn.db).Transact(func(ctx context.Context) error {
			tx := postgres.TxFromContext(ctx, lsn.db)
			if _, err = lsn.pipelineRunner.InsertFinishedRun(postgres.UnwrapGorm(tx), run, true); err != nil {
				return err
			}
			if err = lsn.logBroadcaster.MarkConsumed(tx, req.lb); err != nil {
				return err
			}
			_, err = lsn.txm.CreateEthTransaction(tx, bulletprooftxmanager.NewTx{
				FromAddress:    fromAddress,
				ToAddress:      lsn.coordinator.Address(),
				EncodedPayload: hexutil.MustDecode(payload),
				GasLimit:       gaslimit,
				Meta: &bulletprooftxmanager.EthTxMeta{
					RequestID: common.BytesToHash(req.req.RequestId.Bytes()),
					MaxLink:   bi.String(),
				},
				MinConfirmations: null.Uint32From(uint32(lsn.cfg.MinRequiredOutgoingConfirmations())),
				Strategy:         bulletprooftxmanager.NewSendEveryStrategy(false), // We already simd
			})
			// TODO: maybe save the eth tx id somewhere to link it
			return err
		})
		if err != nil {
			// TODO: log error
			continue
		}
		// If we successfully enqueued for the bptxm, subtract that balance
		// And loop to attempt to enqueue another fulfillment
		startBalance = startBalance.Sub(startBalance, bi)
		processed[req.req.RequestId.String()] = struct{}{}
	}
	// Remove all the confirmed logs
	lsn.reqsMu.Lock()
	var toKeep []pendingRequest
	for _, req := range reqs {
		if _, ok := processed[req.req.RequestId.String()]; !ok {
			toKeep = append(toKeep, req)
		}
	}
	lsn.reqs = toKeep
	lsn.reqsMu.Unlock()
	lsn.l.Infow("VRFListenerV2: finished processing for sub",
		"sub", reqs[0].req.SubId,
		"total reqs", len(reqs),
		"total processed", len(processed))
}

// Here we use the pipeline to parse the log, generate a vrf response
// then simulate the transaction at the max gas price to determine its maximum link cost.
func (lsn *listenerV3) getMaxLinkForFulfillment(maxGasPrice *big.Int, req pendingRequest) (*big.Int, pipeline.Run, string, uint64, error) {
	var (
		maxLink  *big.Int
		payload  string
		gaslimit uint64
	)
	vars := pipeline.NewVarsFrom(map[string]interface{}{
		"jobSpec": map[string]interface{}{
			"databaseID":    lsn.job.ID,
			"externalJobID": lsn.job.ExternalJobID,
			"name":          lsn.job.Name.ValueOrZero(),
			"publicKey":     lsn.job.VRFSpec.PublicKey[:],
			"maxGasPrice":   maxGasPrice.String(),
		},
		"jobRun": map[string]interface{}{
			"logBlockHash":   req.req.Raw.BlockHash[:],
			"logBlockNumber": req.req.Raw.BlockNumber,
			"logTxHash":      req.req.Raw.TxHash,
			"logTopics":      req.req.Raw.Topics,
			"logData":        req.req.Raw.Data,
		},
	})
	run, trrs, err := lsn.pipelineRunner.ExecuteRun(context.Background(), *lsn.job.PipelineSpec, vars, lsn.l)
	if err != nil {
		logger.Errorw("VRFListenerV2: failed executing run", "err", err)
		return maxLink, run, payload, gaslimit, err
	}
	// The call task will fail if there are insufficient funds
	if run.Errors.HasError() {
		logger.Warn("VRFListenerV2: simulation errored, possibly insufficient funds. Request will remain unprocessed until funds are available", "err", err, "max gas price", maxGasPrice)
		return maxLink, run, payload, gaslimit, errors.New("run errored")
	}
	if len(trrs.FinalResult().Values) != 1 {
		logger.Errorw("VRFListenerV2: unexpected number of outputs", "err", err)
		return maxLink, run, payload, gaslimit, errors.New("unexpected number of outputs")
	}
	// Run succeeded, we expect a byte array representing the billing amount
	b, ok := trrs.FinalResult().Values[0].([]uint8)
	if !ok {
		logger.Errorw("VRFListenerV2: unexpected type")
		return maxLink, run, payload, gaslimit, errors.New("expected []uint8 final result")
	}
	maxLink = utils.HexToBig(hexutil.Encode(b)[2:])
	for _, trr := range trrs {
		if trr.Task.Type() == pipeline.TaskTypeVRFV2 {
			m := trr.Result.Value.(map[string]interface{})
			payload = m["output"].(string)
		}
		if trr.Task.Type() == pipeline.TaskTypeEstimateGasLimit {
			gaslimit = trr.Result.Value.(uint64)
		}
	}
	return maxLink, run, payload, gaslimit, nil
}

func (lsn *listenerV3) runRequestHandler() {
	// TODO: Probably would have to be a configuration parameter per job so chains could have faster ones
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-lsn.chStop:
			lsn.waitOnStop <- struct{}{}
			return
		case <-tick.C:
			lsn.processPendingVRFRequests()
		}
	}
}

func (lsn *listenerV3) runLogListener(unsubscribes []func(), minConfs uint32) {
	lsn.l.Infow("VRFListenerV2: listening for run requests",
		"minConfs", minConfs)
	for {
		select {
		case <-lsn.chStop:
			for _, f := range unsubscribes {
				f()
			}
			lsn.waitOnStop <- struct{}{}
			return
		case <-lsn.reqLogs.Notify():
			// Process all the logs in the queue if one is added
			for {
				i, exists := lsn.reqLogs.Retrieve()
				if !exists {
					break
				}
				lb, ok := i.(log.Broadcast)
				if !ok {
					panic(fmt.Sprintf("VRFListenerV2: invariant violated, expected log.Broadcast got %T", i))
				}
				lsn.handleLog(lb, minConfs)
			}
		}
	}
}

func (lsn *listenerV3) shouldProcessLog(lb log.Broadcast) bool {
	ctx, cancel := postgres.DefaultQueryCtx()
	defer cancel()
	consumed, err := lsn.logBroadcaster.WasAlreadyConsumed(lsn.db.WithContext(ctx), lb)
	if err != nil {
		lsn.l.Errorw("VRFListenerV2: could not determine if log was already consumed", "error", err, "txHash", lb.RawLog().TxHash)
		// Do not process, let lb resend it as a retry mechanism.
		return false
	}
	return !consumed
}

func (lsn *listenerV3) getConfirmedAt(req *vrf_coordinator_v2.VRFCoordinatorV2RandomWordsRequested, minConfs uint32) uint64 {
	lsn.respCountMu.Lock()
	defer lsn.respCountMu.Unlock()
	newConfs := uint64(minConfs) * (1 << lsn.respCount[req.RequestId.String()])
	// We cap this at 200 because solidity only supports the most recent 256 blocks
	// in the contract so if it was older than that, fulfillments would start failing
	// without the blockhash store feeder. We use 200 to give the node plenty of time
	// to fulfill even on fast chains.
	if newConfs > 200 {
		newConfs = 200
	}
	if lsn.respCount[req.RequestId.String()] > 0 {
		lsn.l.Warnw("VRFListenerV2: duplicate request found after fulfillment, doubling incoming confirmations",
			"txHash", req.Raw.TxHash,
			"blockNumber", req.Raw.BlockNumber,
			"blockHash", req.Raw.BlockHash,
			"reqID", req.RequestId.String(),
			"newConfs", newConfs)
	}
	return req.Raw.BlockNumber + newConfs
}

func (lsn *listenerV3) handleLog(lb log.Broadcast, minConfs uint32) {
	if v, ok := lb.DecodedLog().(*vrf_coordinator_v2.VRFCoordinatorV2RandomWordsFulfilled); ok {
		lsn.l.Infow("Received fulfilled log", "reqID", v.RequestId, "success", v.Success)
		if !lsn.shouldProcessLog(lb) {
			return
		}
		lsn.respCountMu.Lock()
		lsn.respCount[v.RequestId.String()]++
		lsn.respCountMu.Unlock()
		lsn.blockNumberToReqID.Insert(fulfilledReqV2{
			blockNumber: v.Raw.BlockNumber,
			reqID:       v.RequestId.String(),
		})
		lsn.markLogAsConsumed(lb)
		return
	}

	req, err := lsn.coordinator.ParseRandomWordsRequested(lb.RawLog())
	if err != nil {
		lsn.l.Errorw("VRFListenerV2: failed to parse log", "err", err, "txHash", lb.RawLog().TxHash)
		if !lsn.shouldProcessLog(lb) {
			return
		}
		lsn.markLogAsConsumed(lb)
		return
	}

	confirmedAt := lsn.getConfirmedAt(req, minConfs)
	lsn.reqsMu.Lock()
	lsn.reqs = append(lsn.reqs, pendingRequest{
		confirmedAtBlock: confirmedAt,
		req:              req,
		lb:               lb,
	})
	lsn.reqAdded()
	lsn.reqsMu.Unlock()
}

func (lsn *listenerV3) markLogAsConsumed(lb log.Broadcast) {
	ctx, cancel := postgres.DefaultQueryCtx()
	defer cancel()
	err := lsn.logBroadcaster.MarkConsumed(lsn.db.WithContext(ctx), lb)
	lsn.l.ErrorIf(errors.Wrapf(err, "VRFListenerV2: unable to mark log %v as consumed", lb.String()))
}

// Close complies with job.Service
func (lsn *listenerV3) Close() error {
	return lsn.StopOnce("VRFListenerV2", func() error {
		close(lsn.chStop)
		<-lsn.waitOnStop
		return nil
	})
}

func (lsn *listenerV3) HandleLog(lb log.Broadcast) {
	wasOverCapacity := lsn.reqLogs.Deliver(lb)
	if wasOverCapacity {
		logger.Error("VRFListenerV2: log mailbox is over capacity - dropped the oldest log")
	}
}

// Job complies with log.Listener
func (lsn *listenerV3) JobID() int32 {
	return lsn.job.ID
}