// SPDX-FileCopyrightText: 2022 Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: Apache-2.0
package drsm

import (
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/omec-project/util/logger"
	MongoDBLibrary "github.com/omec-project/util/mongoapi"
	ipam "github.com/thakurajayL/go-ipam"
	"go.mongodb.org/mongo-driver/bson"
)

type chunkState int

const (
	Invalid chunkState = iota + 1
	Owned
	PeerOwned
	Orphan
	Scanning
)

type chunk struct {
	Id              int64
	Owner           PodId
	State           chunkState
	FreeIds         []int64
	AllocIds        map[int64]bool
	ScanIds         []int64
	stopScan        chan bool
	resourceValidCb func(int64) bool
}

type podData struct {
	mu            sync.Mutex       `bsin:"-" json:"-"`
	PodId         PodId            `bson:"podId,omitempty" json:"podId,omitempty"`
	Timestamp     time.Time        `bson:"time,omitempty" json:"time,omitempty"`
	PrevTimestamp time.Time        `bson:"-" json:"-"`
	podChunks     map[int64]*chunk `bson:"-" json:"-"` // chunkId to Chunk
}

type Drsm struct {
	mu                  sync.Mutex
	sharedPoolName      string
	clientId            PodId
	db                  DbInfo
	mode                DrsmMode
	resIdSize           int64
	localChunkTbl       map[int64]*chunk    // chunkid to chunk
	globalChunkTbl      map[int64]*chunk    // chunkid to chunk
	podMap              map[string]*podData // podId to podData
	podDown             chan string
	scanChunks          map[int64]*chunk
	chunkIdRange        int64
	resourceValidCb     func(int64) bool
	ipModule            ipam.Ipamer
	prefix              map[string]*ipam.Prefix
	mongo               *MongoDBLibrary.MongoClient
	globalChunkTblMutex sync.Mutex
}

func (d *Drsm) DeletePod(podInstance string) {
	filter := bson.M{"type": "keepalive", "podInstance": podInstance}
	d.mongo.RestfulAPIDeleteMany(d.sharedPoolName, filter)
	logger.AppLog.Infoln("Deleted PodId from DB: ", podInstance)
}

func (d *Drsm) ConstuctDrsm(opt *Options) {
	if opt != nil {
		d.mode = opt.Mode
		logger.AppLog.Debugln("drsm mode set to ", d.mode)
		if opt.ResIdSize > 0 {
			d.resIdSize = opt.ResIdSize
		} else {
			d.resIdSize = 24
		}
		d.resourceValidCb = opt.ResourceValidCb
	}
	d.chunkIdRange = 1 << (d.resIdSize - 10)
	log.Printf("ChunkId in the range of 0 to %v ", d.chunkIdRange)
	d.localChunkTbl = make(map[int64]*chunk)
	d.globalChunkTbl = make(map[int64]*chunk)
	d.podMap = make(map[string]*podData)
	d.podDown = make(chan string, 10)
	d.scanChunks = make(map[int64]*chunk)
	d.globalChunkTblMutex = sync.Mutex{}
	t := time.Now().UnixNano()
	rand.Seed(t)
	d.initIpam(opt)

	//connect to DB
	d.mongo, _ = MongoDBLibrary.NewMongoClient(d.db.Url, d.db.Name)
	logger.AppLog.Debugln("MongoClient is created.", d.db.Name)

	go d.handleDbUpdates()
	go d.punchLiveness()
	go d.podDownDetected()
	go d.checkAllChunks()
}
