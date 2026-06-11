package catalogimport

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestAuditPerCallTaskDetectsDurationOvercharge(t *testing.T) {
	task := model.Task{
		TaskID: "task-overcharged",
		UserId: 7,
		Quota:  3000000,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				OriginModelName: "grok-video-3-10s",
				ModelPrice:      0.6,
				GroupRatio:      1,
				OtherRatios:     map[string]float64{"seconds": 10},
			},
		},
	}

	row, affected := auditPerCallTask(task, 500000)
	if !affected {
		t.Fatal("expected overcharged task to be reported")
	}
	if row.ExpectedQuota != 300000 || row.ActualQuota != 3000000 || row.DifferenceQuota != 2700000 {
		t.Fatalf("unexpected audit row: %+v", row)
	}
}
