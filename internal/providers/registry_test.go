package providers

import (
	"reflect"
	"testing"
)

func TestListIncludesEveryRegisteredDescriptor(t *testing.T) {
	listed := List()
	seen := make(map[string]struct{}, len(listed))
	for _, descriptor := range listed {
		if _, duplicate := seen[descriptor.ID]; duplicate {
			t.Fatalf("duplicate provider %q", descriptor.ID)
		}
		seen[descriptor.ID] = struct{}{}
	}
	for id := range registry {
		if _, ok := seen[id]; !ok {
			t.Fatalf("provider %q is registered but missing from List", id)
		}
	}
}

// TestLookupAgreesWithList 锁定 Get() 与 List() 派生自同源的这一不变量。
// 在此之前这两个面是手工维护的，因此 provider 可能被加入一个面而
// 被遗忘在另一个面——要么在 /api/providers 中不可见，要么无法按 ID 解析。
// 两个方向都被断言，外加描述符相等性，使部分编辑无法蒙混过关。
func TestLookupAgreesWithList(t *testing.T) {
	listed := List()
	if len(listed) != len(registry) {
		t.Fatalf("List has %d providers but the lookup index has %d", len(listed), len(registry))
	}
	for _, descriptor := range listed {
		got, ok := Get(descriptor.ID)
		if !ok {
			t.Fatalf("provider %q is listed but not resolvable via Get", descriptor.ID)
		}
		if !reflect.DeepEqual(got, descriptor) {
			t.Fatalf("provider %q differs between List and Get:\n list=%+v\n get =%+v", descriptor.ID, descriptor, got)
		}
	}
}

// TestListReturnsACopy 保护共享描述符集合免受调用方修改：
// List() 必须交出副本，这样调用方对切片排序或编辑就不会破坏
// 进程余下部分的 registry。
func TestListReturnsACopy(t *testing.T) {
	first := List()
	if len(first) == 0 {
		t.Fatal("expected at least one built-in provider")
	}
	originalID, originalLabel := first[0].ID, first[0].Label
	first[0].ID = "mutated"
	first[0].Label = "mutated"
	again := List()
	if again[0].ID != originalID || again[0].Label != originalLabel {
		t.Fatalf("List leaked its backing array: got %q/%q, want %q/%q",
			again[0].ID, again[0].Label, originalID, originalLabel)
	}
}
