package memory

import (
	"errors"
	"fmt"

	"smem/internal/workspace"
)

func Show(layout workspace.Layout, ref string) (Record, error) {
	if !validReference(ref) {
		return Record{}, errors.New("无效的记忆引用")
	}
	records, err := readRecords(layout, "all")
	if err != nil {
		return Record{}, err
	}

	var found Record
	count := 0
	for _, record := range records {
		if record.Ref == ref {
			found = record
			count++
		}
	}
	switch count {
	case 0:
		return Record{}, errors.New("记忆引用已失效或不存在")
	case 1:
		return found, nil
	default:
		return Record{}, errors.New("记忆引用发生碰撞，拒绝歧义读取")
	}
}

func List(layout workspace.Layout, scope string, offset, limit int) ([]Record, bool, error) {
	if scope != "all" && scope != "project" && scope != "global" {
		return nil, false, fmt.Errorf("无效的记忆范围：%q", scope)
	}
	if offset < 0 {
		return nil, false, errors.New("offset 不能小于 0")
	}
	if limit < 1 || limit > 100 {
		return nil, false, errors.New("limit 必须在 1 到 100 之间")
	}
	maxInt := int(^uint(0) >> 1)
	if offset > maxInt-limit {
		return nil, false, errors.New("分页范围溢出")
	}

	records, err := readRecords(layout, scope)
	if err != nil {
		return nil, false, err
	}
	if offset >= len(records) {
		return []Record{}, false, nil
	}
	end := offset + limit
	if end > len(records) {
		end = len(records)
	}
	page := append([]Record(nil), records[offset:end]...)
	return page, end < len(records), nil
}
