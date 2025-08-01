//go:build fts5

// Package fts implents sqlite FTS5 tokenizer for json arrays (~tags) and html.
package fts

/*
#include "sqlite3.h"
#include "stdlib.h"
#include "string.h"
#include <stdio.h>
#ifdef C_SHARED_BUILD
#include <sqlite3ext.h>
SQLITE_EXTENSION_INIT1
#endif

extern int callTokenize(char *zName, void *pCtx, int flags, char *pText, int nText, void* cb);
extern char* getTokenizers();
typedef int (*xToken)(void*, int, const char*, int, int, int);

extern char* callProcess(char *zName, char *text, int *indices, int n_indices);
extern char* getProcessFuncs();

static int tokenizer_create(void* pCtx, const char** azArg, int nArg, Fts5Tokenizer** ppOut) {
    *ppOut = (Fts5Tokenizer*)pCtx;
    return SQLITE_OK;
}

static void tokenizer_delete(Fts5Tokenizer* pTok) {
    sqlite3_free(pTok);
}

static int tokenizer_tokenize(Fts5Tokenizer* pTok, void* pCtx, int flags, const char* pText, int nText, xToken cb) {
    return callTokenize((char*)pTok, pCtx, flags, (char*)pText, nText, (void*)cb);
    }

static void process(const Fts5ExtensionApi *pApi, Fts5Context *pFts, sqlite3_context *pCtx, int nVal, sqlite3_value **apVal) {
    char *zName = (char*)pApi->xUserData(pFts);
    int *aMatch = 0;
    int *aPhraseSize = 0;
    if (nVal != 1) {
        sqlite3_result_error(pCtx, "function requires exactly 1 argument (col_idx)", -1);
        return;
    }
    if (sqlite3_value_type(apVal[0]) != SQLITE_INTEGER) {
        sqlite3_result_error(pCtx, "column index arg must be an int", -1);
        return;
    }
    int iCol = sqlite3_value_int(apVal[0]);
    const char *column_text = 0;
    int n_text = 0;
    int rc = pApi->xColumnText(pFts, iCol, &column_text, &n_text);
    if (rc != SQLITE_OK) {
        sqlite3_result_error_code(pCtx, rc);
        return;
    }
    int nPhrase = pApi->xPhraseCount(pFts);
    if (nPhrase > 0) {
        aPhraseSize = (int*)malloc(sizeof(int) * nPhrase);
        if (aPhraseSize == 0) {
            sqlite3_result_error_nomem(pCtx);
            return;
        }
        for (int i = 0; i < nPhrase; i++) {
            aPhraseSize[i] = pApi->xPhraseSize(pFts, i);
        }
    }
    int nInst = 0;
    rc = pApi->xInstCount(pFts, &nInst);
    if (rc != SQLITE_OK) {
        free(aPhraseSize);
        sqlite3_result_error_code(pCtx, rc);
        return;
    }
    aMatch = (int*)malloc(sizeof(int) * nInst * 2);
    if (aMatch == 0) {
        free(aPhraseSize);
        sqlite3_result_error_nomem(pCtx);
        return;
    }
    int nMatch = 0;
    for (int i = 0; i < nInst; i++) {
        int iPhrase, iInstCol, iPos;
        rc = pApi->xInst(pFts, i, &iPhrase, &iInstCol, &iPos);
        if (rc != SQLITE_OK) {
            free(aPhraseSize);
            free(aMatch);
            sqlite3_result_error_code(pCtx, rc);
            return;
        }
        if (iInstCol == iCol) {
            aMatch[nMatch * 2] = iPos;
            aMatch[nMatch * 2 + 1] = aPhraseSize[iPhrase];
            nMatch++;
        }
    }
    if (nMatch > 0) {
        char *zResult = callProcess(zName, (char*)column_text, aMatch, nMatch);
        if (zResult) {
            sqlite3_result_text(pCtx, zResult, -1, free);
        } else {
          sqlite3_result_text(pCtx, column_text, -1, SQLITE_TRANSIENT);
        }
    } else {
      sqlite3_result_text(pCtx, "", 0, SQLITE_STATIC);
    }
    free(aPhraseSize);
    free(aMatch);
}

int sqlite3_extension_init(sqlite3 *db, char **pzErrMsg, const sqlite3_api_routines *pApi) {
    #ifdef C_SHARED_BUILD
    SQLITE_EXTENSION_INIT2(pApi)
    #endif
    fts5_api *pFtsApi = 0;
    sqlite3_stmt *pStmt = 0;
    sqlite3_prepare_v2(db, "SELECT fts5(?1)", -1, &pStmt, 0);
    sqlite3_bind_pointer(pStmt, 1, &pFtsApi, "fts5_api_ptr", 0);
    sqlite3_step(pStmt);
    sqlite3_finalize(pStmt);
    if (!pFtsApi) return SQLITE_ERROR;
    static fts5_tokenizer t = {tokenizer_create, tokenizer_delete, tokenizer_tokenize};
    char *zNames = getTokenizers(), *zWalk = zNames;
    for (; *zWalk; zWalk += strlen(zWalk) + 1) {
        char *pName = sqlite3_mprintf("%s", zWalk);
        pFtsApi->xCreateTokenizer(pFtsApi, pName, (void*)pName, &t, 0);
    }
    free(zNames);
    char *zProcessFuncNames = getProcessFuncs(), *zWalkPf = zProcessFuncNames;
    for (; *zWalkPf; zWalkPf += strlen(zWalkPf) + 1) {
        char *pName = sqlite3_mprintf("%s", zWalkPf);
        pFtsApi->xCreateFunction(pFtsApi, pName, (void*)pName, process, sqlite3_free);
    }
    free(zProcessFuncNames);
    return SQLITE_OK;
}

#ifndef C_SHARED_BUILD
static void __attribute__((constructor)) init() {
    sqlite3_auto_extension((void*)sqlite3_extension_init);
}
#endif
*/
import "C"
import "fmt"

type Tokenizer = func(text string, flags int, cb func(token string, flags, start, end int) error) error
type Processor = func(text string, indexes [][2]int) string

type TokenizeFlag int

const (
	TokenizeQuery    = C.FTS5_TOKENIZE_QUERY
	TokenizePrefix   = C.FTS5_TOKENIZE_PREFIX
	TokenizeDocument = C.FTS5_TOKENIZE_DOCUMENT
	TokenizeAux      = C.FTS5_TOKENIZE_AUX
)

var tokenizers = map[string]Tokenizer{}
var processors = map[string]Processor{}

func init() {
	Register("json", JSON)
	Register("html", HTML)
	Register("html_snippet", NewHTMLSnippetProcessor(5, 3, "<mark>", "</mark>", " … "))
}

func Register(name string, v any) {
	if ft, ok := v.(Tokenizer); ok {
		tokenizers[name] = ft
	} else if fp, ok := v.(Processor); ok {
		processors[name] = fp
	} else {
		panic(fmt.Sprintf("Unsupported type: %T", v))
	}
}
