package op

import (
	"context"
	"path"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
)

func UploadProxyCompleted(ctx context.Context, storage driver.Driver, dir, name string) {
	Cache.linkCache.DeleteKey(Key(storage, path.Join(dir, name)))
	Cache.DeleteDirectory(storage, dir)
	Cache.InvalidateStorageDetails(storage)
	if needHandleObjsUpdateHook() {
		go objsUpdateHook(context.WithoutCancel(ctx), storage, dir, false)
	}
}
