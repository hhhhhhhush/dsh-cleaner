// 批量操作：删除全部已归档、清空未分组（原工作区已被删除的孤儿会话）
package store

import (
	"fmt"

	"dsh-cleaner/internal/config"
)

// RemoveArchived 删除全部处于归档状态的会话
func RemoveArchived(paths config.Paths, hard bool) (*RemoveResult, error) {
	snap, err := Scan(paths)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range snap.Sessions {
		if s.Archived {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return &RemoveResult{}, fmt.Errorf("当前没有已归档会话")
	}
	return RemoveSessions(paths, ids, hard)
}

// RemoveOrphans 清空未分组：磁盘上有会话目录，但索引里已无归属
func RemoveOrphans(paths config.Paths, hard bool) (*RemoveResult, error) {
	snap, err := Scan(paths)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range snap.Sessions {
		if !s.InIndex {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return &RemoveResult{}, fmt.Errorf("当前没有未分组会话")
	}
	return RemoveSessions(paths, ids, hard)
}
