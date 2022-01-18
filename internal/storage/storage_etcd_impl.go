// MIT License
// 
// (C) Copyright [2022] Hewlett Packard Enterprise Development LP
// 
// Permission is hereby granted, free of charge, to any person obtaining a
// copy of this software and associated documentation files (the "Software"),
// to deal in the Software without restriction, including without limitation
// the rights to use, copy, modify, merge, publish, distribute, sublicense,
// and/or sell copies of the Software, and to permit persons to whom the
// Software is furnished to do so, subject to the following conditions:
// 
// The above copyright notice and this permission notice shall be included
// in all copies or substantial portions of the Software.
// 
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL
// THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
// OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
// ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
// OTHER DEALINGS IN THE SOFTWARE.

package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	hmetcd "github.com/Cray-HPE/hms-hmetcd"
	"github.com/Cray-HPE/hms-xname/xnametypes"
)

// This file contains interface functions for the ETCD implementation of PCS 
// storage.   It will also be used for the in-memory implementation, indirectly,
// since the HMS ETCD package already provides both ETCD and in-memory 
// implementations.

const (
	kvUrlMemDefault  = "mem:"
	kvUrlDefault     = kvUrlMemDefault	//Default to in-memory implementation
	kvRetriesDefault = 5
	keyPrefix        = "/pcs/"
	keySegPowerState    = "/powerstate"
	keyMin           = " "
	keyMax           = "~"
)

type ETCDStorage struct {
	Logger   *logrus.Logger
	mutex    *sync.Mutex
	kvHandle hmetcd.Kvi
}

func (e *ETCDStorage) fixUpKey(k string) string {
	key := k
	if !strings.HasPrefix(k, keyPrefix) {
		key = keyPrefix
		if strings.HasPrefix(k, "/") {
			key += k[1:]
		} else {
			key += k
		}
	}
	return key
}

////// ETCD /////

func (e *ETCDStorage) kvStore(key string, val interface{}) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	data, err := json.Marshal(val)
	if err == nil {
		realKey := e.fixUpKey(key)
		err = e.kvHandle.Store(realKey, string(data))
	}
	return err
}

func (e *ETCDStorage) kvGet(key string, val interface{}) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	realKey := e.fixUpKey(key)
	v, exists, err := e.kvHandle.Get(realKey)
	if exists {
		// We have a key, so val is valid.
		err = json.Unmarshal([]byte(v), &val)
	} else if err == nil {
		// No key and no error.  We will return this condition as an error
		err = fmt.Errorf("Key %s does not exist", key)
	}
	return err
}

//if a key doesnt exist, etcd doesn't return an error
func (e *ETCDStorage) kvDelete(key string) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	realKey := e.fixUpKey(key)
	e.Logger.Trace("delete" + realKey)
	return e.kvHandle.Delete(e.fixUpKey(key))
}

func (e *ETCDStorage) Init(Logger *logrus.Logger) error {
	var kverr error

	if (Logger == nil) {
		e.Logger = logrus.New()
	} else {
		e.Logger = Logger
	}

	e.mutex = &sync.Mutex{}
	retries := kvRetriesDefault
	host, hostExists := os.LookupEnv("ETCD_HOST")
	if !hostExists {
		e.kvHandle = nil
		return fmt.Errorf("No ETCD HOST specified, can't open ETCD.")
	}
	port, portExists := os.LookupEnv("ETCD_PORT")
	if !portExists {
		e.kvHandle = nil
		return fmt.Errorf("No ETCD PORT specified, can't open ETCD.")
	}

	kvURL := fmt.Sprintf("http://%s:%s", host, port)
	e.Logger.Info(kvURL)

	etcOK := false
	for ix := 1; ix <= retries; ix++ {
		e.kvHandle, kverr = hmetcd.Open(kvURL, "")
		if kverr != nil {
			e.Logger.Error("ERROR opening connection to ETCD (attempt ", ix, "):", kverr)
		} else {
			etcOK = true
			e.Logger.Info("ETCD connection succeeded.")
			break
		}
	}
	if !etcOK {
		e.kvHandle = nil
		return fmt.Errorf("ETCD connection attempts exhausted, can't connect.")
	}
	return nil
}

func (e *ETCDStorage) Ping() error {
	e.Logger.Debug("ETCD PING")
	key := fmt.Sprintf("/ping/%s", uuid.New().String())
	err := e.kvStore(key, "")
	if err == nil {
		err = e.kvDelete(key)
	}
	return err
}

func (e *ETCDStorage) StorePowerStatus(p PowerStatusComponent) error {
	if !(xnametypes.IsHMSCompIDValid(p.XName)) {
		return fmt.Errorf("Error parsing '%s': invalid xname format.",p.XName)
	}
	key := fmt.Sprintf("%s/%s", keySegPowerState,p.XName)
	err := e.kvStore(key, p)
	if err != nil {
		e.Logger.Error(err)
	}
	return err
}

func (e *ETCDStorage) DeletePowerStatus(xname string) error {
	if !(xnametypes.IsHMSCompIDValid(xname)) {
		return fmt.Errorf("Error parsing '%s': invalid xname format.",xname)
	}
	key := fmt.Sprintf("%s/%s", keySegPowerState,xname)
	err := e.kvDelete(key)
	if err != nil {
		e.Logger.Error(err)
	}
	return err
}

func (e *ETCDStorage) GetPowerStatus(xname string) (PowerStatusComponent, error) {
	var pcomp PowerStatusComponent
	if !(xnametypes.IsHMSCompIDValid(xname)) {
		return pcomp,fmt.Errorf("Error parsing '%s': invalid xname format.",xname)
	}
	key := fmt.Sprintf("%s/%s", keySegPowerState,xname)

	err := e.kvGet(key, &pcomp)
	if err != nil {
		e.Logger.Error(err)
	}
	return pcomp, err
}

func (e *ETCDStorage) GetAllPowerStatus() (PowerStatus, error) {
	var pstats PowerStatus
	k := e.fixUpKey(keySegPowerState)
	kvl, err := e.kvHandle.GetRange(k+keyMin, k+keyMax)
	if err == nil {
		for _, kv := range kvl {
			var pcomp PowerStatusComponent
			err = json.Unmarshal([]byte(kv.Value), &pcomp)
			if err != nil {
				e.Logger.Error(err)
			} else {
				pstats.Status = append(pstats.Status, pcomp)
			}
		}
	} else {
		e.Logger.Error(err)
	}
	return pstats, err
}

