package sqlitetune

/*
#include <stdlib.h>

typedef struct sqlite3 sqlite3;
typedef struct sqlite3_context sqlite3_context;
typedef struct sqlite3_value sqlite3_value;
typedef struct sqlite3_blob sqlite3_blob;
typedef struct sqlite3_api_routines sqlite3_api_routines;
// Match the bundled SQLite header's sqlite3_int64 ABI on supported compilers.
typedef long long int history_sqlite_int64;

extern int sqlite3_auto_extension(void (*)(void));
extern int sqlite3_create_function_v2(sqlite3*, const char*, int, int, void*,
    void (*)(sqlite3_context*, int, sqlite3_value**), void*, void*, void (*)(void*));
extern sqlite3 *sqlite3_context_db_handle(sqlite3_context*);
extern int sqlite3_value_type(sqlite3_value*);
extern history_sqlite_int64 sqlite3_value_int64(sqlite3_value*);
extern int sqlite3_blob_open(sqlite3*, const char*, const char*, const char*,
    history_sqlite_int64, int, sqlite3_blob**);
extern int sqlite3_blob_bytes(sqlite3_blob*);
extern int sqlite3_blob_read(sqlite3_blob*, void*, int, int);
extern int sqlite3_blob_close(sqlite3_blob*);
extern void sqlite3_result_blob(sqlite3_context*, const void*, int, void (*)(void*));
extern void sqlite3_result_error(sqlite3_context*, const char*, int);
extern void sqlite3_result_zeroblob(sqlite3_context*, int);

#define HISTORY_SQLITE_OK 0
#define HISTORY_SQLITE_INTEGER 1
#define HISTORY_SQLITE_UTF8 1
#define HISTORY_SQLITE_DIRECTONLY 0x000080000
#define HISTORY_CHUNK_BYTES 16384

// Open a read-only SQLite blob on the current connection and statement
// snapshot; the caller can select only these two fixed columns.
static void history_blob_chunk(sqlite3_context *ctx, int argc, sqlite3_value **argv) {
    if (argc != 3 || sqlite3_value_type(argv[0]) != HISTORY_SQLITE_INTEGER ||
        sqlite3_value_type(argv[1]) != HISTORY_SQLITE_INTEGER ||
        sqlite3_value_type(argv[2]) != HISTORY_SQLITE_INTEGER) {
        sqlite3_result_error(ctx, "invalid history blob parameters", -1);
        return;
    }
    history_sqlite_int64 kind = sqlite3_value_int64(argv[0]);
    history_sqlite_int64 rowid = sqlite3_value_int64(argv[1]);
    history_sqlite_int64 offset = sqlite3_value_int64(argv[2]);
    if ((kind != 0 && kind != 1) || offset < 0) {
        sqlite3_result_error(ctx, "invalid history blob parameters", -1);
        return;
    }
    const char *table = kind == 0 ? "tasks" : "task_results";
    const char *column = kind == 0 ? "command" : "result";
    sqlite3_blob *blob = NULL;
    int rc = sqlite3_blob_open(sqlite3_context_db_handle(ctx), "main", table,
                               column, rowid, 0, &blob);
    if (rc != HISTORY_SQLITE_OK) {
        sqlite3_result_error(ctx, "cannot open history blob", -1);
        return;
    }
    int size = sqlite3_blob_bytes(blob);
    if (offset >= size) {
        rc = sqlite3_blob_close(blob);
        if (rc == HISTORY_SQLITE_OK) sqlite3_result_zeroblob(ctx, 0);
        else sqlite3_result_error(ctx, "cannot close history blob", -1);
        return;
    }
    int count = size - (int)offset;
    if (count > HISTORY_CHUNK_BYTES) count = HISTORY_CHUNK_BYTES;
    unsigned char data[HISTORY_CHUNK_BYTES];
    rc = sqlite3_blob_read(blob, data, count, (int)offset);
    int close_rc = sqlite3_blob_close(blob);
    if (rc != HISTORY_SQLITE_OK || close_rc != HISTORY_SQLITE_OK) {
        sqlite3_result_error(ctx, "cannot read history blob", -1);
        return;
    }
    sqlite3_result_blob(ctx, data, count, (void (*)(void*))-1);
}

static int register_history_blob(sqlite3 *db, char **error, const sqlite3_api_routines *api) {
    (void)error;
    (void)api;
    return sqlite3_create_function_v2(db, "nekomari_history_blob", 3,
        HISTORY_SQLITE_UTF8 | HISTORY_SQLITE_DIRECTONLY, NULL, history_blob_chunk,
        NULL, NULL, NULL);
}

static int register_history_blob_auto_extension(void) {
    return sqlite3_auto_extension((void (*)(void))register_history_blob);
}
*/
import "C"

import "fmt"

// SQLite runs this C-only extension for each connection, including test
// databases. DIRECTONLY prevents stored views, triggers or schema expressions
// from invoking it. Authorization still belongs to the history RPC.
func init() {
	if rc := int(C.register_history_blob_auto_extension()); rc != 0 {
		panic(fmt.Sprintf("register SQLite history blob extension: code %d", rc))
	}
}
