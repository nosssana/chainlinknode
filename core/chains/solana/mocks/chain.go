// Code generated by mockery v2.10.0. DO NOT EDIT.

package mocks

import (
	client "github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
	config "github.com/smartcontractkit/chainlink-solana/pkg/solana/config"

	context "context"

	mock "github.com/stretchr/testify/mock"

	solana "github.com/smartcontractkit/chainlink-solana/pkg/solana"
)

// Chain is an autogenerated mock type for the Chain type
type Chain struct {
	mock.Mock
}

// Close provides a mock function with given fields:
func (_m *Chain) Close() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Config provides a mock function with given fields:
func (_m *Chain) Config() config.Config {
	ret := _m.Called()

	var r0 config.Config
	if rf, ok := ret.Get(0).(func() config.Config); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(config.Config)
		}
	}

	return r0
}

// Healthy provides a mock function with given fields:
func (_m *Chain) Healthy() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// ID provides a mock function with given fields:
func (_m *Chain) ID() string {
	ret := _m.Called()

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// Reader provides a mock function with given fields: nodeName
func (_m *Chain) Reader(nodeName string) (client.Reader, error) {
	ret := _m.Called(nodeName)

	var r0 client.Reader
	if rf, ok := ret.Get(0).(func(string) client.Reader); ok {
		r0 = rf(nodeName)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.Reader)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(nodeName)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Ready provides a mock function with given fields:
func (_m *Chain) Ready() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Start provides a mock function with given fields: _a0
func (_m *Chain) Start(_a0 context.Context) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// TxManager provides a mock function with given fields:
func (_m *Chain) TxManager() solana.TxManager {
	ret := _m.Called()

	var r0 solana.TxManager
	if rf, ok := ret.Get(0).(func() solana.TxManager); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(solana.TxManager)
		}
	}

	return r0
}
