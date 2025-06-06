// Copyright 2015 The Xorm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.8
// +build go1.8

package xorm

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/grafana/grafana/pkg/util/xorm/core"
)

const (
	// Version show the xorm's version
	Version string = "0.8.0.1015"
)

func regDrvsNDialects() bool {
	providedDrvsNDialects := map[string]struct {
		dbType     core.DbType
		getDriver  func() core.Driver
		getDialect func() core.Dialect
	}{
		"mysql":    {"mysql", func() core.Driver { return &mysqlDriver{} }, func() core.Dialect { return &mysql{} }},
		"mymysql":  {"mysql", func() core.Driver { return &mymysqlDriver{} }, func() core.Dialect { return &mysql{} }},
		"postgres": {"postgres", func() core.Driver { return &pqDriver{} }, func() core.Dialect { return &postgres{} }},
		"pgx":      {"postgres", func() core.Driver { return &pqDriverPgx{} }, func() core.Dialect { return &postgres{} }},
		"sqlite3":  {"sqlite3", func() core.Driver { return &sqlite3Driver{} }, func() core.Dialect { return &sqlite3{} }},
	}

	for driverName, v := range providedDrvsNDialects {
		if driver := core.QueryDriver(driverName); driver == nil {
			core.RegisterDriver(driverName, v.getDriver())
			core.RegisterDialect(v.dbType, v.getDialect)
		}
	}
	return true
}

func close(engine *Engine) {
	engine.Close()
}

func init() {
	regDrvsNDialects()
}

// NewEngine new a db manager according to the parameter. Currently support four
// drivers
func NewEngine(driverName string, dataSourceName string) (*Engine, error) {
	driver := core.QueryDriver(driverName)
	if driver == nil {
		return nil, fmt.Errorf("unsupported driver name: %v", driverName)
	}

	uri, err := driver.Parse(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	dialect := core.QueryDialect(uri.DbType)
	if dialect == nil {
		return nil, fmt.Errorf("unsupported dialect type: %v", uri.DbType)
	}

	db, err := core.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	err = dialect.Init(db, uri, driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	engine := &Engine{
		db:              db,
		dialect:         dialect,
		Tables:          make(map[reflect.Type]*core.Table),
		mutex:           &sync.RWMutex{},
		TagIdentifier:   "xorm",
		TZLocation:      time.Local,
		tagHandlers:     defaultTagHandlers,
		defaultContext:  context.Background(),
		timestampFormat: "2006-01-02 15:04:05",
		randomIDGen:     newSnowflake(rand.Int64N(1024)).Generate,
	}

	switch uri.DbType {
	case core.SQLITE:
		engine.DatabaseTZ = time.UTC
	default:
		engine.DatabaseTZ = time.Local
	}

	logger := NewSimpleLogger(os.Stdout)
	logger.SetLevel(core.LOG_INFO)
	engine.SetLogger(logger)
	engine.SetMapper(core.NewCacheMapper(new(core.SnakeMapper)))

	runtime.SetFinalizer(engine, close)

	if ext, ok := dialect.(DialectWithSequenceGenerator); ok {
		engine.sequenceGenerator, err = ext.CreateSequenceGenerator(db.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create sequence generator: %w", err)
		}
	}

	return engine, nil
}

func (engine *Engine) ResetSequenceGenerator() {
	if engine.sequenceGenerator != nil {
		engine.sequenceGenerator.Reset()
	}
}

type SequenceGenerator interface {
	Next(ctx context.Context, table, column string) (int64, error)
	Reset()
}

type DialectWithSequenceGenerator interface {
	core.Dialect

	// CreateSequenceGenerator returns optional generator used to create AUTOINCREMENT ids for inserts.
	CreateSequenceGenerator(db *sql.DB) (SequenceGenerator, error)
}

type DialectWithRetryableErrors interface {
	core.Dialect
	RetryOnError(err error) bool
}
