//go:build fts5

package fts

/*
#include <sqlite3ext.h>
#include <stdlib.h>
typedef int (*xToken)(void*, int, const char*, int, int, int);
static inline int call_xToken(void* pCb, void* pCtx, int flags, const char* pToken, int nToken, int iStart, int iEnd) {
    return ((xToken)pCb)(pCtx, flags, pToken, nToken, iStart, iEnd);
}
*/
import "C"
import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/exp/maps"
)

//export getTokenizers
func getTokenizers() *C.char {
	return C.CString(strings.Join(maps.Keys(tokenizers), "\x00") + "\x00")
}

//export callTokenize
func callTokenize(pName *C.char, pCtx unsafe.Pointer, flags C.int, pText *C.char, nText C.int, cb unsafe.Pointer) C.int {
	f, text := tokenizers[C.GoString(pName)], C.GoStringN(pText, nText)
	err := f(text, int(flags), func(token string, flags, start, end int) error {
		cToken := C.CString(token)
		defer C.free(unsafe.Pointer(cToken))
		rc := C.call_xToken(cb, pCtx, C.int(flags), cToken, C.int(len(token)), C.int(start), C.int(end))
		if rc != C.SQLITE_OK {
			return fmt.Errorf("xToken failed with code %d", rc)
		}
		return nil
	})
	if err != nil {
		return C.SQLITE_ERROR
	}
	return C.SQLITE_OK
}

//export getProcessFuncs
func getProcessFuncs() *C.char {
	return C.CString(strings.Join(maps.Keys(processors), "\x00") + "\x00")
}

//export callProcess
func callProcess(zName *C.char, cText *C.char, indices *C.int, nIndices C.int) *C.char {
	f, text := processors[C.GoString(zName)], C.GoString(cText)
	idxs := [][2]int{}
	matches := (*[1 << 28]C.int)(unsafe.Pointer(indices))[: int(nIndices)*2 : int(nIndices)*2]
	for i := 0; i < int(nIndices); i++ {
		token, l := int(matches[i*2]), int(matches[i*2+1])
		idxs = append(idxs, [2]int{token, l})
	}
	return C.CString(f(text, idxs))
}
