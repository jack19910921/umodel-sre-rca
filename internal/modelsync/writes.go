package modelsync

import (
	"fmt"
	"time"

	sls "github.com/aliyun/aliyun-log-go-sdk"
)

// entityStoreWrite is one classified, validated SLS Log Protocol batch. The
// logstore is derived only from the reviewed workspace and record kind.
type entityStoreWrite struct {
	logstore    string
	group       *sls.LogGroup
	description string
}

func buildEntityStoreWrites(workspace string, plan Plan, observedAt time.Time) ([]entityStoreWrite, error) {
	if !workspacePattern.MatchString(workspace) {
		return nil, fmt.Errorf("unsafe workspace name %q", workspace)
	}
	entities, relations := splitEntityStorePlan(plan)
	writes := make([]entityStoreWrite, 0, 2)
	if len(entities.Elements) > 0 {
		group, err := buildEntityStoreLogGroup(entities, observedAt)
		if err != nil {
			return nil, err
		}
		writes = append(writes, entityStoreWrite{
			logstore: entityStoreEntityLogStoreName(workspace), group: group,
			description: "write UModel EntityStore entity data",
		})
	}
	if len(relations.Elements) > 0 {
		group, err := buildEntityStoreRelationLogGroup(relations, observedAt)
		if err != nil {
			return nil, err
		}
		writes = append(writes, entityStoreWrite{
			logstore: entityStoreTopoLogStoreName(workspace), group: group,
			description: "write UModel EntityStore relation data",
		})
	}
	return writes, nil
}
