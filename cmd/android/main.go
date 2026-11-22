//go:build android

// The Android shared library uses explicit JNI functions, not an HTTP listener.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unsafe"

	"github.com/revamptori/Horizon-manager/internal/core"
)

var mu sync.RWMutex
var engine *core.Engine

func envelope(result any, err error) *C.char {
	var bytes []byte
	if err != nil {
		bytes, _ = json.Marshal(map[string]string{"error": err.Error()})
	} else {
		bytes, _ = json.Marshal(map[string]any{"result": result})
	}
	return C.CString(string(bytes))
}

//export HorizonInit
func HorizonInit(path *C.char) *C.char {
	mu.Lock()
	defer mu.Unlock()
	if engine != nil {
		return envelope(nil, nil)
	}
	var err error
	engine, err = core.New(core.Options{DataDir: C.GoString(path), Platform: "android"})
	return envelope(nil, err)
}

//export HorizonCall
func HorizonCall(request *C.char) *C.char {
	raw := C.GoString(request)
	if len(raw) > 2<<20 {
		return envelope(nil, errors.New("native request exceeds 2 MB"))
	}
	var value struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return envelope(nil, err)
	}
	mu.RLock()
	host := engine
	mu.RUnlock()
	if host == nil {
		return envelope(nil, errors.New("Android Go core is not initialized"))
	}
	result, err := host.Invoke(context.Background(), value.Method, value.Params)
	return envelope(result, err)
}

//export HorizonShutdown
func HorizonShutdown() {
	mu.RLock()
	host := engine
	mu.RUnlock()
	if host != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		host.Close(ctx)
	}
}

//export HorizonFree
func HorizonFree(value *C.char) { C.free(unsafe.Pointer(value)) }
func main()                     {}
