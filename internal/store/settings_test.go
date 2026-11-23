package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSettingsCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openTestStore(t)
	t1 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	if _, err := st.GetSetting(ctx, "instance_name"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSetting missing err = %v, want ErrNotFound", err)
	}

	if err := st.SetSetting(ctx, "instance_name", `"Acme Mailcubby"`, t1); err != nil {
		t.Fatalf("SetSetting insert: %v", err)
	}
	got, err := st.GetSetting(ctx, "instance_name")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got.Value != `"Acme Mailcubby"` || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("GetSetting = %+v, want value %q at %v", got, `"Acme Mailcubby"`, t1)
	}

	// Upsert updates value and timestamp.
	if err := st.SetSetting(ctx, "instance_name", `"Updated"`, t2); err != nil {
		t.Fatalf("SetSetting upsert: %v", err)
	}
	if err := st.SetSetting(ctx, "audit_check", `true`, t2); err != nil {
		t.Fatalf("SetSetting second: %v", err)
	}

	all, err := st.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(all) != 2 || all[0].Key != "audit_check" || all[1].Key != "instance_name" || all[1].Value != `"Updated"` {
		t.Fatalf("ListSettings = %+v", all)
	}

	if err := st.DeleteSetting(ctx, "audit_check"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if err := st.DeleteSetting(ctx, "audit_check"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteSetting second err = %v, want ErrNotFound", err)
	}
}
