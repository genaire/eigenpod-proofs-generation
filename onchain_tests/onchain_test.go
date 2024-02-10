package onchain_tests

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"testing"

	eigenpodproofs "github.com/Layr-Labs/eigenpod-proofs-generation"
	beacon "github.com/Layr-Labs/eigenpod-proofs-generation/beacon"
	contractBeaconChainProofs "github.com/Layr-Labs/eigenpod-proofs-generation/bindings"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/assert"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	chainClient                    *eigenpodproofs.ChainClient
	ctx                            context.Context
	contractAddress                common.Address
	beaconChainProofs              *contractBeaconChainProofs.BeaconChainProofs
	oracleState                    deneb.BeaconState
	oracleBlockHeader              phase0.BeaconBlockHeader
	blockHeader                    phase0.BeaconBlockHeader
	blockHeaderIndex               uint64
	block                          deneb.BeaconBlock
	validatorIndex                 phase0.ValidatorIndex
	beaconBlockHeaderToVerifyIndex uint64
	executionPayload               deneb.ExecutionPayload
	epp                            *eigenpodproofs.EigenPodProofs
	executionPayloadFieldRoots     []phase0.Root
)

const GOERLI_CHAIN_ID = uint64(5)
const VALIDATOR_INDEX = uint64(61336)
const DENEB_FORK_TIMESTAMP_GOERLI = uint64(1705473120)

func TestMain(m *testing.M) {
	// Setup
	log.Println("Setting up suite")
	setupSuite()

	// Run tests
	code := m.Run()

	// Teardown
	log.Println("Tearing down suite")
	teardownSuite()

	// Exit with test result code
	os.Exit(code)
}

func setupSuite() {
	rpc := os.Getenv("RPC_URL")
	privateKey := os.Getenv("PRIVATE_KEY")

	ethClient, err := ethclient.Dial(rpc)
	if err != nil {
		log.Panicf("failed to connect to the Ethereum client: %s", err)
	}

	chainClient, err = eigenpodproofs.NewChainClient(ethClient, privateKey)
	if err != nil {
		log.Panicf("failed to create chain client: %s", err)
	}
	ctx = context.Background()
	//BeaconChainProofs.sol deployment: https://goerli.etherscan.io/address/0xcb5e2cbd8df189aff1e94cf471a869e220e95c85#code
	contractAddress = common.HexToAddress("0xCb5e2Cbd8dF189aFF1e94CF471A869E220E95C85")
	beaconChainProofs, err = contractBeaconChainProofs.NewBeaconChainProofs(contractAddress, chainClient)
	if err != nil {
		log.Panicf("failed to create contract instance: %s", err)
	}

	log.Println("Setting up suite")
	stateFile := "../data/deneb_goerli_slot_7413760.json"
	oracleHeaderFile := "../data/deneb_goerli_block_header_7413760.json"
	headerFile := "../data/deneb_goerli_block_header_7426113.json"
	bodyFile := "../data/deneb_goerli_block_7426113.json"

	stateJSON, err := eigenpodproofs.ParseJSONFileDeneb(stateFile)
	if err != nil {
		fmt.Println("error with JSON parsing beacon state")
	}
	eigenpodproofs.ParseDenebBeaconStateFromJSON(*stateJSON, &oracleState)

	blockHeader, err = eigenpodproofs.ExtractBlockHeader(headerFile)
	if err != nil {
		fmt.Println("error with block header", err)
	}

	oracleBlockHeader, err = eigenpodproofs.ExtractBlockHeader(oracleHeaderFile)
	if err != nil {
		fmt.Println("error with oracle block header", err)
	}

	block, err = eigenpodproofs.ExtractBlockDeneb(bodyFile)
	if err != nil {
		fmt.Println("error with block body", err)
	}

	executionPayload = *block.Body.ExecutionPayload

	blockHeaderIndex = uint64(blockHeader.Slot) % beacon.SlotsPerHistoricalRoot

	epp, err = eigenpodproofs.NewEigenPodProofs(GOERLI_CHAIN_ID, 1000)
	if err != nil {
		fmt.Println("error in NewEigenPodProofs", err)
	}

	executionPayloadFieldRoots, _ = beacon.ComputeExecutionPayloadFieldRootsDeneb(block.Body.ExecutionPayload)
}

func teardownSuite() {
	// Any cleanup you want to perform should go here
	fmt.Println("all done!")
}

func TestValidatorContainersProofOnChain(t *testing.T) {

	versionedOracleState, err := beacon.CreateVersionedState(&oracleState)
	if err != nil {
		fmt.Println("error", err)
	}

	verifyValidatorFieldsCallParams, err := epp.ProveValidatorContainers(&oracleBlockHeader, &versionedOracleState, []uint64{VALIDATOR_INDEX})
	if err != nil {
		fmt.Println("error", err)
	}

	validatorFieldsProof := verifyValidatorFieldsCallParams.ValidatorFieldsProofs[0].ToByteSlice()
	validatorIndex := new(big.Int).SetUint64(verifyValidatorFieldsCallParams.ValidatorIndices[0])
	oracleBlockHeaderRoot, err := oracleBlockHeader.HashTreeRoot()
	if err != nil {
		fmt.Println("error", err)
	}

	err = beaconChainProofs.VerifyStateRootAgainstLatestBlockRoot(
		&bind.CallOpts{},
		oracleBlockHeaderRoot,
		verifyValidatorFieldsCallParams.StateRootProof.BeaconStateRoot,
		verifyValidatorFieldsCallParams.StateRootProof.StateRootProof.ToByteSlice(),
	)
	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)

	var validatorFields [][32]byte
	for _, field := range verifyValidatorFieldsCallParams.ValidatorFields[0] {
		validatorFields = append(validatorFields, field)
	}

	err = beaconChainProofs.VerifyValidatorFields(
		&bind.CallOpts{},
		verifyValidatorFieldsCallParams.StateRootProof.BeaconStateRoot,
		validatorFields,
		validatorFieldsProof,
		validatorIndex,
	)
	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)
}

func TestContractCall(t *testing.T) {

	err := beaconChainProofs.VerifyWithdrawal(
		&bind.CallOpts{},
		phase0.Root{},
		[][32]byte{},
		contractBeaconChainProofs.BeaconChainProofsWithdrawalProof{},
		DENEB_FORK_TIMESTAMP_GOERLI,
	)

	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)
}

func TestProvingDenebWithdrawalAgainstDenebStateOnChain(t *testing.T) {

	oracleStateFile := "../data/deneb_goerli_slot_7431952.json"
	oracleStateJSON, err := eigenpodproofs.ParseJSONFileDeneb(oracleStateFile)
	if err != nil {
		fmt.Println("error with JSON parsing beacon state")
	}
	oracleState := deneb.BeaconState{}
	eigenpodproofs.ParseDenebBeaconStateFromJSON(*oracleStateJSON, &oracleState)

	versionedOracleState, err := beacon.CreateVersionedState(&oracleState)
	if err != nil {
		fmt.Println("error creating versioned state", err)
	}

	historicalSummaryStateJSON, err := eigenpodproofs.ParseJSONFileDeneb("../data/deneb_goerli_slot_7421952.json")
	if err != nil {
		fmt.Println("error parsing historicalSummaryState JSON")
	}
	var historicalSummaryState deneb.BeaconState
	eigenpodproofs.ParseDenebBeaconStateFromJSON(*historicalSummaryStateJSON, &historicalSummaryState)
	historicalSummaryStateBlockRoots := historicalSummaryState.BlockRoots

	withdrawalBlock, err := eigenpodproofs.ExtractBlockDeneb("../data/deneb_goerli_block_7421951.json")
	if err != nil {
		fmt.Println("block.UnmarshalJSON error", err)
	}

	versionedWithdrawalBlock, err := beacon.CreateVersionedSignedBlock(withdrawalBlock)
	if err != nil {
		fmt.Println("error", err)
	}

	withdrawalValidatorIndex := uint64(627559) //this is the index of the validator with the first withdrawal in the withdrawalBlock 7421951

	verifyAndProcessWithdrawalCallParams, err := epp.ProveWithdrawals(
		&oracleBlockHeader,
		&versionedOracleState,
		[][]phase0.Root{historicalSummaryStateBlockRoots},
		[]*spec.VersionedSignedBeaconBlock{&versionedWithdrawalBlock},
		[]uint64{withdrawalValidatorIndex},
	)
	if err != nil {
		fmt.Println("error", err)
	}

	var withdrawalFields [][32]byte
	for _, field := range verifyAndProcessWithdrawalCallParams.WithdrawalFields[0] {
		withdrawalFields = append(withdrawalFields, field)
	}

	withdrawalProof := contractBeaconChainProofs.BeaconChainProofsWithdrawalProof{
		WithdrawalProof:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalProof.ToByteSlice(),
		SlotProof:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotProof.ToByteSlice(),
		ExecutionPayloadProof:           verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadProof.ToByteSlice(),
		TimestampProof:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampProof.ToByteSlice(),
		HistoricalSummaryBlockRootProof: verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryBlockRootProof.ToByteSlice(),
		BlockRootIndex:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRootIndex,
		HistoricalSummaryIndex:          verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryIndex,
		WithdrawalIndex:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalIndex,
		BlockRoot:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRoot,
		SlotRoot:                        verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotRoot,
		TimestampRoot:                   verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampRoot,
		ExecutionPayloadRoot:            verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadRoot,
	}

	withdrawalFields = append(withdrawalFields, verifyAndProcessWithdrawalCallParams.WithdrawalFields[0][0])

	beaconChainProofs.VerifyWithdrawal(
		&bind.CallOpts{},
		verifyAndProcessWithdrawalCallParams.StateRootProof.BeaconStateRoot,
		withdrawalFields,
		withdrawalProof,
		DENEB_FORK_TIMESTAMP_GOERLI,
	)

	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)
}

func TestProvingCapellaWithdrawalAgainstDenebStateOnChain(t *testing.T) {

	oracleStateFile := "../data/deneb_goerli_slot_7431952.json"
	oracleStateJSON, err := eigenpodproofs.ParseJSONFileDeneb(oracleStateFile)
	if err != nil {
		fmt.Println("error with JSON parsing beacon state")
	}
	oracleState := deneb.BeaconState{}
	eigenpodproofs.ParseDenebBeaconStateFromJSON(*oracleStateJSON, &oracleState)

	versionedOracleState, err := beacon.CreateVersionedState(&oracleState)
	if err != nil {
		fmt.Println("error creating versioned state", err)
	}

	historicalSummaryStateJSON, err := eigenpodproofs.ParseJSONFileCapella("../data/goerli_slot_6397952.json")
	if err != nil {
		fmt.Println("error parsing historicalSummaryState JSON")
	}
	var historicalSummaryState capella.BeaconState
	eigenpodproofs.ParseCapellaBeaconStateFromJSON(*historicalSummaryStateJSON, &historicalSummaryState)
	historicalSummaryStateBlockRoots := historicalSummaryState.BlockRoots

	withdrawalBlock, err := eigenpodproofs.ExtractBlockCapella("../data/goerli_block_6397852.json")
	if err != nil {
		fmt.Println("block.UnmarshalJSON error", err)
	}

	versionedWithdrawalBlock, err := beacon.CreateVersionedSignedBlock(withdrawalBlock)
	if err != nil {
		fmt.Println("error", err)
	}

	withdrawalValidatorIndex := uint64(200240) //this is the index of the validator with the first withdrawal in the withdrawalBlock 7421951

	verifyAndProcessWithdrawalCallParams, err := epp.ProveWithdrawals(
		&oracleBlockHeader,
		&versionedOracleState,
		[][]phase0.Root{historicalSummaryStateBlockRoots},
		[]*spec.VersionedSignedBeaconBlock{&versionedWithdrawalBlock},
		[]uint64{withdrawalValidatorIndex},
	)
	if err != nil {
		fmt.Println("error", err)
	}

	var withdrawalFields [][32]byte
	for _, field := range verifyAndProcessWithdrawalCallParams.WithdrawalFields[0] {
		withdrawalFields = append(withdrawalFields, field)
	}

	withdrawalProof := contractBeaconChainProofs.BeaconChainProofsWithdrawalProof{
		WithdrawalProof:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalProof.ToByteSlice(),
		SlotProof:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotProof.ToByteSlice(),
		ExecutionPayloadProof:           verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadProof.ToByteSlice(),
		TimestampProof:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampProof.ToByteSlice(),
		HistoricalSummaryBlockRootProof: verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryBlockRootProof.ToByteSlice(),
		BlockRootIndex:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRootIndex,
		HistoricalSummaryIndex:          verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryIndex,
		WithdrawalIndex:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalIndex,
		BlockRoot:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRoot,
		SlotRoot:                        verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotRoot,
		TimestampRoot:                   verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampRoot,
		ExecutionPayloadRoot:            verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadRoot,
	}

	err = beaconChainProofs.VerifyWithdrawal(
		&bind.CallOpts{},
		verifyAndProcessWithdrawalCallParams.StateRootProof.BeaconStateRoot,
		withdrawalFields,
		withdrawalProof,
		DENEB_FORK_TIMESTAMP_GOERLI,
	)
	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)
}

func TestProvingCapellaWithdrawalAgainstCapellaStateOnChain(t *testing.T) {
	oracleStateFile := "../data/goerli_slot_6409723.json"
	oracleStateJSON, err := eigenpodproofs.ParseJSONFileCapella(oracleStateFile)
	if err != nil {
		fmt.Println("error with JSON parsing beacon state")
	}
	oracleState := capella.BeaconState{}
	eigenpodproofs.ParseCapellaBeaconStateFromJSON(*oracleStateJSON, &oracleState)

	versionedOracleState, err := beacon.CreateVersionedState(&oracleState)
	if err != nil {
		fmt.Println("error creating versioned state", err)
	}

	historicalSummaryStateJSON, err := eigenpodproofs.ParseJSONFileCapella("../data/goerli_slot_6397952.json")
	if err != nil {
		fmt.Println("error parsing historicalSummaryState JSON")
	}
	var historicalSummaryState capella.BeaconState
	eigenpodproofs.ParseCapellaBeaconStateFromJSON(*historicalSummaryStateJSON, &historicalSummaryState)
	historicalSummaryStateBlockRoots := historicalSummaryState.BlockRoots

	withdrawalBlock, err := eigenpodproofs.ExtractBlockCapella("../data/goerli_block_6397852.json")
	if err != nil {
		fmt.Println("block.UnmarshalJSON error", err)
	}

	versionedWithdrawalBlock, err := beacon.CreateVersionedSignedBlock(withdrawalBlock)
	if err != nil {
		fmt.Println("error", err)
	}

	withdrawalValidatorIndex := uint64(200240) //this is the index of the validator with the first withdrawal in the withdrawalBlock 7421951

	verifyAndProcessWithdrawalCallParams, err := epp.ProveWithdrawals(
		&oracleBlockHeader,
		&versionedOracleState,
		[][]phase0.Root{historicalSummaryStateBlockRoots},
		[]*spec.VersionedSignedBeaconBlock{&versionedWithdrawalBlock},
		[]uint64{withdrawalValidatorIndex},
	)
	if err != nil {
		fmt.Println("error", err)
	}

	var withdrawalFields [][32]byte
	for _, field := range verifyAndProcessWithdrawalCallParams.WithdrawalFields[0] {
		withdrawalFields = append(withdrawalFields, field)
	}

	withdrawalProof := contractBeaconChainProofs.BeaconChainProofsWithdrawalProof{
		WithdrawalProof:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalProof.ToByteSlice(),
		SlotProof:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotProof.ToByteSlice(),
		ExecutionPayloadProof:           verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadProof.ToByteSlice(),
		TimestampProof:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampProof.ToByteSlice(),
		HistoricalSummaryBlockRootProof: verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryBlockRootProof.ToByteSlice(),
		BlockRootIndex:                  verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRootIndex,
		HistoricalSummaryIndex:          verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].HistoricalSummaryIndex,
		WithdrawalIndex:                 verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].WithdrawalIndex,
		BlockRoot:                       verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].BlockRoot,
		SlotRoot:                        verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].SlotRoot,
		TimestampRoot:                   verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].TimestampRoot,
		ExecutionPayloadRoot:            verifyAndProcessWithdrawalCallParams.WithdrawalProofs[0].ExecutionPayloadRoot,
	}

	err = beaconChainProofs.VerifyWithdrawal(
		&bind.CallOpts{},
		verifyAndProcessWithdrawalCallParams.StateRootProof.BeaconStateRoot,
		withdrawalFields,
		withdrawalProof,
		DENEB_FORK_TIMESTAMP_GOERLI,
	)
	if err != nil {
		fmt.Println("error", err)
	}
	assert.Nil(t, err)

}