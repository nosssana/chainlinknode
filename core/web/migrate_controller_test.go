package web_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gopkg.in/guregu/null.v4"

	"github.com/smartcontractkit/chainlink/core/adapters"
	"github.com/smartcontractkit/chainlink/core/assets"
	"github.com/smartcontractkit/chainlink/core/internal/cltest"
	clnull "github.com/smartcontractkit/chainlink/core/null"
	"github.com/smartcontractkit/chainlink/core/services/job"
	"github.com/smartcontractkit/chainlink/core/services/keystore/keys/ethkey"
	"github.com/smartcontractkit/chainlink/core/store/models"
	"github.com/smartcontractkit/chainlink/core/store/presenters"
	webpresenters "github.com/smartcontractkit/chainlink/core/web/presenters"
)

func TestMigrateController_MigrateRunLog(t *testing.T) {

	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	config.Set("ETH_DISABLED", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_FLUX_MONITOR_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)

	// Create the v1 job
	resp, cleanup := client.Post("/v2/specs", strings.NewReader(`
{
  "name": "QDT Price Prediction",
  "initiators": [
    {
      "id": 2,
      "jobSpecId": "3f6c38d0-a080-424a-b18e-a3ef05099ea1",
      "type": "runlog",
      "params": {
        "address": "0xfe8f390ffd3c74870367121ce251c744d3dc01ed",
			  "requesters": ["0xfe8F390fFD3c74870367121cE251C744d3DC01Ed","0xae8F390fFD3c74870367121cE251C744d3DC01Ed"]
      }
    }
  ],
  "tasks": [
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "httpget",
      "params": {
        "get": "https://test.com"
      }
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "httppost",
      "params": {
        "post": "https://test.com",
        "body": "{}"
      }
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "testbridge",
      "params": {
        "endpoint": "price"
      }
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "jsonparse",
      "params": {
        "path": "result"
      }
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "multiply",
      "params": {
        "times": 100000000
      }
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "ethuint256"
    },
    {
      "jobSpecId": "3f6c38d0a080424ab18ea3ef05099ea1",
      "type": "ethtx"
    }
  ]
}
`))
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var jobV1 presenters.JobSpec
	cltest.ParseJSONAPIResponse(t, resp, &jobV1)

	expectedDotSpec := `decode_log [
	abi="OracleRequest(bytes32 indexed specId, address requester, bytes32 requestId, uint256 payment, address callbackAddr, bytes4 callbackFunctionId, uint256 cancelExpiration, uint256 dataVersion, bytes data)"
	data="$(jobRun.logData)"
	topics="$(jobRun.logTopics)"
	type=ethabidecodelog
	];
	decode_cbor [
	data="$(decode_log.data)"
	mode=diet
	type=cborparse
	];
	http_get_0 [
	method=GET
	type=http
	url="https://test.com"
	];
	http_post_1 [
	method=POST
	requestData=<{}>
	type=http
	url="https://test.com"
	];
	merge_3 [
	right=<{"endpoint":"price"}>
	type=merge
	];
	send_to_bridge_3 [
	name=testbridge
	requestData=<{ "data": $(merge_3) }>
	type=bridge
	];
	merge_jsonparse_3 [
	left="$(decode_cbor)"
	right=<{ "path": "result" }>
	type=merge
	];
	jsonparse_3 [
	data="$(send_to_bridge_3)"
	path="$(merge_jsonparse_3.path)"
	type=jsonparse
	];
	merge_multiply_4 [
	left="$(decode_cbor)"
	right=<{ "times": "100000000" }>
	type=merge
	];
	multiply_4 [
	input="$(jsonparse_3)"
	times="$(merge_multiply_4.times)"
	type=multiply
	];
	encode_data_7 [
	abi="(uint256 value)"
	data=<{ "value": $(multiply_4) }>
	type=ethabiencode
	];
	encode_tx_7 [
	abi="fulfillOracleRequest(bytes32 requestId, uint256 payment, address callbackAddress, bytes4 callbackFunctionId, uint256 expiration, bytes32 calldata data)"
	data=<{
"requestId":          $(decode_log.requestId),
"payment":            $(decode_log.payment),
"callbackAddress":    $(decode_log.callbackAddr),
"callbackFunctionId": $(decode_log.callbackFunctionId),
"expiration":         $(decode_log.cancelExpiration),
"data":               $(encode_data_7)
}
>
	type=ethabiencode
	];
	send_tx_7 [
	data="$(encode_tx_7)"
	to="0xfe8F390fFD3c74870367121cE251C744d3DC01Ed"
	type=ethtx
	];
	
	// Edge definitions.
	decode_log -> decode_cbor;
	decode_cbor -> http_get_0;
	http_get_0 -> http_post_1;
	http_post_1 -> merge_3;
	merge_3 -> send_to_bridge_3;
	send_to_bridge_3 -> merge_jsonparse_3;
	merge_jsonparse_3 -> jsonparse_3;
	jsonparse_3 -> merge_multiply_4;
	merge_multiply_4 -> multiply_4;
	multiply_4 -> encode_data_7;
	encode_data_7 -> encode_tx_7;
	encode_tx_7 -> send_tx_7;
	`

	// Migrate it
	resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 200, resp.StatusCode)
	var createdJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

	expectedRequesters := models.AddressCollection(make([]common.Address, 0))
	expectedRequesters = append(expectedRequesters, common.HexToAddress("0xfe8F390fFD3c74870367121cE251C744d3DC01Ed"))
	expectedRequesters = append(expectedRequesters, common.HexToAddress("0xae8F390fFD3c74870367121cE251C744d3DC01Ed"))
	contractAddress, _ := ethkey.NewEIP55Address("0xfe8F390fFD3c74870367121cE251C744d3DC01Ed")
	// v2 job migrated should be identical to v1.
	assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
	assert.Equal(t, job.DirectRequest.String(), createdJobV2.Type.String())
	assert.Equal(t, createdJobV2.Name, jobV1.Name)
	require.NotNil(t, createdJobV2.DirectRequestSpec)
	assert.Equal(t, createdJobV2.DirectRequestSpec.ContractAddress, contractAddress)
	assert.Equal(t, createdJobV2.DirectRequestSpec.MinContractPayment, jobV1.MinPayment)
	assert.Equal(t, createdJobV2.DirectRequestSpec.MinIncomingConfirmations, clnull.Uint32From(10))
	assert.Equal(t, createdJobV2.DirectRequestSpec.Requesters, expectedRequesters)
	assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

	// v1 FM job should be archived
	resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 404, resp.StatusCode)
	errs := cltest.ParseJSONAPIErrors(t, resp.Body)
	require.NotNil(t, errs)
	require.Len(t, errs.Errors, 1)
	require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

	// v2 job read should be identical to created.
	resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var migratedJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
	assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
}

func TestMigrateController_MigrateRunLog_MultiMerge(t *testing.T) {
	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	config.Set("ETH_DISABLED", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_FLUX_MONITOR_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)

	// Create the v1 job
	resp, cleanup := client.Post("/v2/specs", strings.NewReader(`
{
    "name": "multi-word",
    "initiators": [
        {
            "id": 3,
            "jobSpecId": "64566152-8ea4-4aa7-b137-ed9f9d54c3d5",
            "type": "runlog",
            "params": {
                "address": "0xc57b33452b4f7bb189bb5afae9cc4aba1f7a4fd8"
            }
        }
    ],
    "tasks": [
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "httpget",
            "params": {
                "get": "https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "jsonparse",
            "params": {
                "path": [
                    "USD"
                ]
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "multiply",
            "params": {
                "times": 100
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "ethuint256"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "resultcollect"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "httpget",
            "params": {
                "get": "https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "jsonparse",
            "params": {
                "path": [
                    "EUR"
                ]
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "multiply",
            "params": {
                "times": 100
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "ethuint256"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "resultcollect"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "httpget",
            "params": {
                "get": "https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "jsonparse",
            "params": {
                "path": [
                    "JPY"
                ]
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "multiply",
            "params": {
                "times": 100
            }
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "ethuint256"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "resultcollect"
        },
        {
            "jobSpecId": "645661528ea44aa7b137ed9f9d54c3d5",
            "type": "ethtx",
            "confirmations": 1,
            "params": {
                "abiEncoding": [
                    "bytes32",
                    "bytes32",
                    "bytes32",
                    "bytes32"
                ]
            }
        }
    ]
}
`))
	require.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var jobV1 presenters.JobSpec
	cltest.ParseJSONAPIResponse(t, resp, &jobV1)

	expectedDotSpec := `decode_log [
	abi="OracleRequest(bytes32 indexed specId, address requester, bytes32 requestId, uint256 payment, address callbackAddr, bytes4 callbackFunctionId, uint256 cancelExpiration, uint256 dataVersion, bytes data)"
	data="$(jobRun.logData)"
	topics="$(jobRun.logTopics)"
	type=ethabidecodelog
	];
	decode_cbor [
	data="$(decode_log.data)"
	mode=diet
	type=cborparse
	];
	http_get_0 [
	method=GET
	type=http
	url="https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
	];
	merge_jsonparse_1 [
	left="$(decode_cbor)"
	right=<{ "path": "USD" }>
	type=merge
	];
	jsonparse_1 [
	data="$(http_get_0)"
	path="$(merge_jsonparse_1.path)"
	type=jsonparse
	];
	merge_multiply_2 [
	left="$(decode_cbor)"
	right=<{ "times": "100" }>
	type=merge
	];
	multiply_2 [
	input="$(jsonparse_1)"
	times="$(merge_multiply_2.times)"
	type=multiply
	];
	http_get_5 [
	method=GET
	type=http
	url="https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
	];
	merge_jsonparse_6 [
	left="$(decode_cbor)"
	right=<{ "path": "EUR" }>
	type=merge
	];
	jsonparse_6 [
	data="$(http_get_5)"
	path="$(merge_jsonparse_6.path)"
	type=jsonparse
	];
	merge_multiply_7 [
	left="$(decode_cbor)"
	right=<{ "times": "100" }>
	type=merge
	];
	multiply_7 [
	input="$(jsonparse_6)"
	times="$(merge_multiply_7.times)"
	type=multiply
	];
	http_get_10 [
	method=GET
	type=http
	url="https://min-api.cryptocompare.com/data/price?fsym=BTC&tsyms=USD,JPY,EUR"
	];
	merge_jsonparse_11 [
	left="$(decode_cbor)"
	right=<{ "path": "JPY" }>
	type=merge
	];
	jsonparse_11 [
	data="$(http_get_10)"
	path="$(merge_jsonparse_11.path)"
	type=jsonparse
	];
	merge_multiply_12 [
	left="$(decode_cbor)"
	right=<{ "times": "100" }>
	type=merge
	];
	multiply_12 [
	input="$(jsonparse_11)"
	times="$(merge_multiply_12.times)"
	type=multiply
	];
	encode_data_16 [
	abi="(uint256 value)"
	data=<{ "value": $(multiply_12) }>
	type=ethabiencode
	];
	encode_tx_16 [
	abi="fulfillOracleRequest(bytes32 requestId, uint256 payment, address callbackAddress, bytes4 callbackFunctionId, uint256 expiration, bytes32 calldata data)"
	data=<{
"requestId":          $(decode_log.requestId),
"payment":            $(decode_log.payment),
"callbackAddress":    $(decode_log.callbackAddr),
"callbackFunctionId": $(decode_log.callbackFunctionId),
"expiration":         $(decode_log.cancelExpiration),
"data":               $(encode_data_16)
}
>
	type=ethabiencode
	];
	send_tx_16 [
	data="$(encode_tx_16)"
	to="0xc57B33452b4F7BB189bB5AfaE9cc4aBa1f7a4FD8"
	type=ethtx
	];

// Edge definitions.
decode_log -> decode_cbor;
decode_cbor -> http_get_0;
http_get_0 -> merge_jsonparse_1;
merge_jsonparse_1 -> jsonparse_1;
jsonparse_1 -> merge_multiply_2;
merge_multiply_2 -> multiply_2;
multiply_2 -> http_get_5;
http_get_5 -> merge_jsonparse_6;
merge_jsonparse_6 -> jsonparse_6;
jsonparse_6 -> merge_multiply_7;
merge_multiply_7 -> multiply_7;
multiply_7 -> http_get_10;
http_get_10 -> merge_jsonparse_11;
merge_jsonparse_11 -> jsonparse_11;
jsonparse_11 -> merge_multiply_12;
merge_multiply_12 -> multiply_12;
multiply_12 -> encode_data_16;
encode_data_16 -> encode_tx_16;
encode_tx_16 -> send_tx_16;
`

	// Migrate it
	resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	require.Equal(t, 200, resp.StatusCode)
	var createdJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

	var expectedRequesters models.AddressCollection
	contractAddress, _ := ethkey.NewEIP55Address("0xc57B33452b4F7BB189bB5AfaE9cc4aBa1f7a4FD8")
	// v2 job migrated should be identical to v1.
	assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
	assert.Equal(t, job.DirectRequest.String(), createdJobV2.Type.String())
	assert.Equal(t, createdJobV2.Name, jobV1.Name)
	require.NotNil(t, createdJobV2.DirectRequestSpec)
	assert.Equal(t, createdJobV2.DirectRequestSpec.ContractAddress, contractAddress)
	assert.Equal(t, createdJobV2.DirectRequestSpec.MinContractPayment, jobV1.MinPayment)
	assert.Equal(t, createdJobV2.DirectRequestSpec.MinIncomingConfirmations, clnull.Uint32From(10))
	assert.Equal(t, createdJobV2.DirectRequestSpec.Requesters, expectedRequesters)
	assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

	// v1 FM job should be archived
	resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 404, resp.StatusCode)
	errs := cltest.ParseJSONAPIErrors(t, resp.Body)
	require.NotNil(t, errs)
	require.Len(t, errs.Errors, 1)
	require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

	// v2 job read should be identical to created.
	resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var migratedJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
	assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
}

func TestMigrateController_MigrateWeb(t *testing.T) {

	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	config.Set("ETH_DISABLED", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_WEBHOOK_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)

	// Create the v1 job
	resp, cleanup := client.Post("/v2/specs", strings.NewReader(`
{
  "name": "",
  "initiators": [
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "web"
    }
  ],
  "tasks": [
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "httpget",
      "params": {
        "get": "https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD",
        "headers": {
          "X_KEY": [
            "..."
          ]
        }
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "jsonparse",
      "params": {
        "path": [
          "data",
          "BNT",
          "quote",
          "USD",
          "price"
        ]
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "multiply",
      "params": {
        "times": 100000000
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethint256"
    }
  ]
}
`))
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var jobV1 presenters.JobSpec
	cltest.ParseJSONAPIResponse(t, resp, &jobV1)

	expectedDotSpec := `http_get_0 [
	method=GET
	type=http
	url="https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD"
	];
	jsonparse_1 [
	data="$(http_get_0)"
	path="data,BNT,quote,USD,price"
	type=jsonparse
	];
	multiply_2 [
	input="$(jsonparse_1)"
	times=100000000
	type=multiply
	];
	
	// Edge definitions.
	http_get_0 -> jsonparse_1;
	jsonparse_1 -> multiply_2;
	`

	// Migrate it
	resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 200, resp.StatusCode)
	var createdJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

	// v2 job migrated should be identical to v1.
	assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
	assert.Equal(t, job.Webhook.String(), createdJobV2.Type.String())
	assert.Equal(t, createdJobV2.Name, jobV1.Name)
	require.NotNil(t, createdJobV2.WebhookSpec)
	assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

	// v1 FM job should be archived
	resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 404, resp.StatusCode)
	errs := cltest.ParseJSONAPIErrors(t, resp.Body)
	require.NotNil(t, errs)
	require.Len(t, errs.Errors, 1)
	require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

	// v2 job read should be identical to created.
	resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var migratedJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
	assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
}

func TestMigrateController_MigrateExternal(t *testing.T) {

	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	config.Set("ETH_DISABLED", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_WEBHOOK_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)
	cltest.CreateExternalInitiatorViaWeb(t, app, `{"name":"some-external-initiator"}`)

	// Create the v1 job
	resp, cleanup := client.Post("/v2/specs", strings.NewReader(`
{
  "name": "jb",
  "initiators": [
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "external",
      "params": {
				"name": "some-external-initiator",
        "body": {"param1": "value"}
			}
    }
  ],
  "tasks": [
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "httpget",
      "params": {
        "get": "https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD",
        "headers": {
          "X_KEY": [
            "..."
          ]
        }
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "jsonparse",
      "params": {
        "path": [
          "data",
          "BNT",
          "quote",
          "USD",
          "price"
        ]
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "multiply",
      "params": {
        "times": 100000000
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethint256"
    }
  ]
}
`))
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var jobV1 presenters.JobSpec
	cltest.ParseJSONAPIResponse(t, resp, &jobV1)

	expectedDotSpec := `http_get_0 [
	method=GET
	type=http
	url="https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD"
	];
	jsonparse_1 [
	data="$(http_get_0)"
	path="data,BNT,quote,USD,price"
	type=jsonparse
	];
	multiply_2 [
	input="$(jsonparse_1)"
	times=100000000
	type=multiply
	];
	
	// Edge definitions.
	http_get_0 -> jsonparse_1;
	jsonparse_1 -> multiply_2;
	`

	// Migrate it
	resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 200, resp.StatusCode)
	var createdJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

	// v2 job migrated should be identical to v1.
	assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
	assert.Equal(t, job.Webhook.String(), createdJobV2.Type.String())
	assert.Equal(t, createdJobV2.Name, jobV1.Name)
	require.NotNil(t, createdJobV2.WebhookSpec)
	assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

	// v1 FM job should be archived
	resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 404, resp.StatusCode)
	errs := cltest.ParseJSONAPIErrors(t, resp.Body)
	require.NotNil(t, errs)
	require.Len(t, errs.Errors, 1)
	require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

	// v2 job read should be identical to created.
	resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var migratedJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
	assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
}

func TestMigrateController_MigrateCron(t *testing.T) {

	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	config.Set("ETH_DISABLED", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_CRON_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)

	// Create the v1 job
	resp, cleanup := client.Post("/v2/specs", strings.NewReader(`
{
  "name": "",
  "initiators": [
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "cron",
			"params": { "schedule": "CRON_TZ=UTC * * * * *" }
    }
  ],
  "tasks": [
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "httpget",
      "params": {
        "get": "https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD",
        "headers": {
          "X_KEY": [
            "..."
          ]
        }
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "jsonparse",
      "params": {
        "path": [
          "data",
          "BNT",
          "quote",
          "USD",
          "price"
        ]
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethbool"
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethbytes32"
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "multiply",
      "params": {
        "times": 100000000
      }
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "copy"
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "resultcollect"
    },
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethint256"
    }
  ]
}
`))
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var jobV1 presenters.JobSpec
	cltest.ParseJSONAPIResponse(t, resp, &jobV1)

	expectedDotSpec := `http_get_0 [
	method=GET
	type=http
	url="https://pro-api.coinmarketcap.com/v1/cryptocurrency/quotes/latest?symbol=BNT&convert=USD"
	];
	jsonparse_1 [
	data="$(http_get_0)"
	path="data,BNT,quote,USD,price"
	type=jsonparse
	];
	multiply_4 [
	input="$(jsonparse_1)"
	times=100000000
	type=multiply
	];
	
	// Edge definitions.
	http_get_0 -> jsonparse_1;
	jsonparse_1 -> multiply_4;
	`

	// Migrate it
	resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)

	assert.Equal(t, 200, resp.StatusCode)

	var createdJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

	// v2 job migrated should be identical to v1.
	assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
	assert.Equal(t, job.Cron.String(), createdJobV2.Type.String())
	assert.Equal(t, createdJobV2.Name, jobV1.Name)
	require.NotNil(t, createdJobV2.CronSpec)
	assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

	// v1 FM job should be archived
	resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
	t.Cleanup(cleanup)
	assert.Equal(t, 404, resp.StatusCode)
	errs := cltest.ParseJSONAPIErrors(t, resp.Body)
	require.NotNil(t, errs)
	require.Len(t, errs.Errors, 1)
	require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

	// v2 job read should be identical to created.
	resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
	assert.Equal(t, 200, resp.StatusCode)
	t.Cleanup(cleanup)
	var migratedJobV2 webpresenters.JobResource
	cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
	assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
	assert.Equal(t, "CRON_TZ=UTC 0 * * * * *", migratedJobV2.CronSpec.CronSchedule)
}

func TestMigrateController_MigrateUtilEthTx_encode(t *testing.T) {
	for _, tt := range []struct {
		firstInit  string
		secondInit string
	}{
		{"cron", "web"},
		{"web", "cron"},
	} {
		t.Run(tt.firstInit+"-"+tt.secondInit, func(t *testing.T) {
			config, cfgCleanup := cltest.NewConfig(t)
			t.Cleanup(cfgCleanup)
			config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
			config.Set("ETH_DISABLED", true)
			app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
			t.Cleanup(cleanup)
			app.Config.Set("FEATURE_CRON_V2", true)
			require.NoError(t, app.Start())
			client := app.NewHTTPClient()

			// Create the v1 job
			resp, cleanup := client.Post("/v2/specs", strings.NewReader(fmt.Sprintf(`
{
  "name": "",
  "initiators": [
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "%s",
      "params": { "schedule": "CRON_TZ=UTC * * * * *" }
    },
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "%s",
      "params": { "schedule": "CRON_TZ=UTC * * * * *" }
    }
  ],
  "tasks": [
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethtx",
      "params": {"address": "0xf480B1D3658b8f2642bCe6ABCd7E98B96B2A8fC6", "gasLimit": 2800000, "functionSelector": "requestNewRound()"}
    }
  ]
}
`, tt.firstInit, tt.secondInit)))
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var jobV1 presenters.JobSpec
			cltest.ParseJSONAPIResponse(t, resp, &jobV1)
			expectedDotSpec := `encode_tx_0 [
	abi="requestNewRound()"
	type=ethabiencode
	];
	send_tx_0 [
	data="$(encode_tx_0)"
	gasLimit=2800000
	to="0xf480B1D3658b8f2642bCe6ABCd7E98B96B2A8fC6"
	type=ethtx
	];
	
	// Edge definitions.
	encode_tx_0 -> send_tx_0;
	`

			// Migrate it
			resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)

			assert.Equal(t, 200, resp.StatusCode)

			var createdJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

			// v2 job migrated should be identical to v1.
			assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
			assert.Equal(t, job.Cron.String(), createdJobV2.Type.String())
			assert.Equal(t, createdJobV2.Name, jobV1.Name)
			require.NotNil(t, createdJobV2.CronSpec)
			assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

			// v1 FM job should be archived
			resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)
			assert.Equal(t, 404, resp.StatusCode)
			errs := cltest.ParseJSONAPIErrors(t, resp.Body)
			require.NotNil(t, errs)
			require.Len(t, errs.Errors, 1)
			require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

			// v2 job read should be identical to created.
			resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var migratedJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
			assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
			assert.Equal(t, "CRON_TZ=UTC 0 * * * * *", migratedJobV2.CronSpec.CronSchedule)
		})
	}
}

func TestMigrateController_MigrateUtilEthTx_hex(t *testing.T) {
	for _, tt := range []struct {
		firstInit  string
		secondInit string
	}{
		{"cron", "web"},
		{"web", "cron"},
	} {
		t.Run(tt.firstInit+"-"+tt.secondInit, func(t *testing.T) {
			config, cfgCleanup := cltest.NewConfig(t)
			t.Cleanup(cfgCleanup)
			config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
			config.Set("ETH_DISABLED", true)
			app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
			t.Cleanup(cleanup)
			app.Config.Set("FEATURE_CRON_V2", true)
			require.NoError(t, app.Start())
			client := app.NewHTTPClient()

			// Create the v1 job
			resp, cleanup := client.Post("/v2/specs", strings.NewReader(fmt.Sprintf(`
{
  "name": "",
  "initiators": [
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "%s",
      "params": { "schedule": "CRON_TZ=UTC * * * * *" }
    },
    {
      "id": 52,
      "jobSpecId": "95311d21-7c9f-4f35-b00c-fa32b5ae97e1",
      "type": "%s",
      "params": { "schedule": "CRON_TZ=UTC * * * * *" }
    }
  ],
  "tasks": [
    {
      "jobSpecId": "95311d217c9f4f35b00cfa32b5ae97e1",
      "type": "ethtx",
      "params": {"address": "0xf480B1D3658b8f2642bCe6ABCd7E98B96B2A8fC6", "gasLimit": 2800000, "functionSelector": "0x609ff1bd"}
    }
  ]
}
`, tt.firstInit, tt.secondInit)))
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var jobV1 presenters.JobSpec
			cltest.ParseJSONAPIResponse(t, resp, &jobV1)
			expectedDotSpec := `send_tx_0 [
	data="0x609ff1bd"
	gasLimit=2800000
	to="0xf480B1D3658b8f2642bCe6ABCd7E98B96B2A8fC6"
	type=ethtx
	];
	`

			// Migrate it
			resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)

			assert.Equal(t, 200, resp.StatusCode)

			var createdJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

			// v2 job migrated should be identical to v1.
			assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
			assert.Equal(t, job.Cron.String(), createdJobV2.Type.String())
			assert.Equal(t, createdJobV2.Name, jobV1.Name)
			require.NotNil(t, createdJobV2.CronSpec)
			assert.Equal(t, expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

			// v1 FM job should be archived
			resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)
			assert.Equal(t, 404, resp.StatusCode)
			errs := cltest.ParseJSONAPIErrors(t, resp.Body)
			require.NotNil(t, errs)
			require.Len(t, errs.Errors, 1)
			require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

			// v2 job read should be identical to created.
			resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var migratedJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
			assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
			assert.Equal(t, "CRON_TZ=UTC 0 * * * * *", migratedJobV2.CronSpec.CronSchedule)
		})
	}
}

func TestMigrateController_Migrate(t *testing.T) {
	config, cfgCleanup := cltest.NewConfig(t)
	t.Cleanup(cfgCleanup)
	config.Set("ENABLE_LEGACY_JOB_PIPELINE", true)
	app, cleanup := cltest.NewApplicationWithConfigAndKey(t, config)
	t.Cleanup(cleanup)
	app.Config.Set("FEATURE_FLUX_MONITOR_V2", true)
	require.NoError(t, app.Start())
	client := app.NewHTTPClient()
	cltest.CreateBridgeTypeViaWeb(t, app, `{"name":"testbridge","url":"http://data.com"}`)

	var tt = []struct {
		name            string
		jsr             models.JobSpecRequest
		expectedDotSpec string
	}{
		{
			name: "raw url",
			jsr: models.JobSpecRequest{
				Name: "a v1 fm job",
				Initiators: []models.InitiatorRequest{
					{
						Type: models.InitiatorFluxMonitor,
						InitiatorParams: models.InitiatorParams{
							Address:           common.HexToAddress("0x5A0b54D5dc17e0AadC383d2db43B0a0D3E029c4c"),
							RequestData:       models.JSON{Result: gjson.Parse(`{"data":{"coin":"ETH","market":"USD"}}`)},
							Feeds:             models.JSON{Result: gjson.Parse(`["https://lambda.staging.devnet.tools/bnc/call"]`)},
							Threshold:         0.5,
							AbsoluteThreshold: 0.01,
							IdleTimer: models.IdleTimerConfig{
								Duration: models.MustMakeDuration(2 * time.Minute),
							},
							PollTimer: models.PollTimerConfig{
								Period: models.MustMakeDuration(time.Minute),
							},
							Precision: 2,
						},
					},
				},
				Tasks: []models.TaskSpecRequest{
					{
						Type:   adapters.TaskTypeMultiply,
						Params: models.MustParseJSON([]byte(`{"times":"10"}`)),
					},
					{
						Type: adapters.TaskTypeEthUint256,
					},
					{
						Type: adapters.TaskTypeEthTx,
					},
				},
				MinPayment: assets.NewLink(100),
				StartAt:    null.TimeFrom(time.Now()),
				EndAt:      null.TimeFrom(time.Now().Add(time.Second)),
			},
			expectedDotSpec: `
// Node definitions.
median [type=median];
feed0 [
method=POST
requestData="{\"data\":{\"coin\":\"ETH\",\"market\":\"USD\"}}"
type=http
url="https://lambda.staging.devnet.tools/bnc/call"
];
jsonparse0 [
path="data,result"
type=jsonparse
];
multiply0 [
times=10
type=multiply
];

// Edge definitions.
median -> multiply0;
feed0 -> jsonparse0;
jsonparse0 -> median;
`,
		},
		{
			name: "bridge",
			jsr: models.JobSpecRequest{
				Name: "a bridge v1 fm job",
				Initiators: []models.InitiatorRequest{
					{
						Type: models.InitiatorFluxMonitor,
						InitiatorParams: models.InitiatorParams{
							Address:           common.HexToAddress("0x5A0b54D5dc17e0AadC383d2db43B0a0D3E029c4c"),
							RequestData:       models.JSON{Result: gjson.Parse(`{"data":{"coin":"ETH","market":"USD"}}`)},
							Feeds:             models.JSON{Result: gjson.Parse(`[{"bridge": "testbridge"}]`)},
							Threshold:         0.5,
							AbsoluteThreshold: 0.01,
							IdleTimer: models.IdleTimerConfig{
								Duration: models.MustMakeDuration(2 * time.Minute),
							},
							PollTimer: models.PollTimerConfig{
								Period: models.MustMakeDuration(time.Minute),
							},
							Precision: 2,
						},
					},
				},
				Tasks: []models.TaskSpecRequest{
					{
						Type:   adapters.TaskTypeMultiply,
						Params: models.MustParseJSON([]byte(`{"times":"10"}`)),
					},
					{
						Type: adapters.TaskTypeEthUint256,
					},
					{
						Type: adapters.TaskTypeEthTx,
					},
				},
				MinPayment: assets.NewLink(100),
				StartAt:    null.TimeFrom(time.Now()),
				EndAt:      null.TimeFrom(time.Now().Add(time.Second)),
			},
			expectedDotSpec: `
// Node definitions.
median [type=median];
feed0 [
method=POST
name=testbridge
requestData="{\"data\":{\"coin\":\"ETH\",\"market\":\"USD\"}}"
type=bridge
];
jsonparse0 [
path="data,result"
type=jsonparse
];
multiply0 [
times=10
type=multiply
];

// Edge definitions.
median -> multiply0;
feed0 -> jsonparse0;
jsonparse0 -> median;
`,
		},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Create the v1 job
			b, err := json.Marshal(&tc.jsr)
			require.NoError(t, err)
			resp, cleanup := client.Post("/v2/specs", bytes.NewReader(b))
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var jobV1 presenters.JobSpec
			cltest.ParseJSONAPIResponse(t, resp, &jobV1)

			// Migrate it
			resp, cleanup = client.Post(fmt.Sprintf("/v2/migrate/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)
			assert.Equal(t, 200, resp.StatusCode)
			var createdJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &createdJobV2)

			// v2 job migrated should be identical to v1.
			assert.Equal(t, uint32(1), createdJobV2.SchemaVersion)
			assert.Equal(t, job.FluxMonitor.String(), createdJobV2.Type.String())
			assert.Equal(t, createdJobV2.Name, jobV1.Name)
			require.NotNil(t, createdJobV2.FluxMonitorSpec)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.MinPayment, jobV1.MinPayment)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.AbsoluteThreshold, jobV1.Initiators[0].AbsoluteThreshold)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.Threshold, jobV1.Initiators[0].Threshold)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.IdleTimerDisabled, jobV1.Initiators[0].IdleTimer.Disabled)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.IdleTimerPeriod, jobV1.Initiators[0].IdleTimer.Duration.String())
			assert.Equal(t, createdJobV2.FluxMonitorSpec.PollTimerDisabled, jobV1.Initiators[0].PollTimer.Disabled)
			assert.Equal(t, createdJobV2.FluxMonitorSpec.PollTimerPeriod, jobV1.Initiators[0].PollTimer.Period.String())
			assert.Equal(t, tc.expectedDotSpec, createdJobV2.PipelineSpec.DotDAGSource)

			// v1 FM job should be archived
			resp, cleanup = client.Get(fmt.Sprintf("/v2/specs/%s", jobV1.ID.String()), nil)
			t.Cleanup(cleanup)
			assert.Equal(t, 404, resp.StatusCode)
			errs := cltest.ParseJSONAPIErrors(t, resp.Body)
			require.NotNil(t, errs)
			require.Len(t, errs.Errors, 1)
			require.Equal(t, "JobSpec not found", errs.Errors[0].Detail)

			// v2 job read should be identical to created.
			resp, cleanup = client.Get(fmt.Sprintf("/v2/jobs/%s", createdJobV2.ID), nil)
			assert.Equal(t, 200, resp.StatusCode)
			t.Cleanup(cleanup)
			var migratedJobV2 webpresenters.JobResource
			cltest.ParseJSONAPIResponse(t, resp, &migratedJobV2)
			assert.Equal(t, createdJobV2.PipelineSpec.DotDAGSource, migratedJobV2.PipelineSpec.DotDAGSource)
		})
	}
}
