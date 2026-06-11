package catalogimport

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"
)

type TaskBillingAuditRow struct {
	TaskID          string `json:"task_id"`
	UserID          int    `json:"user_id"`
	ModelName       string `json:"model_name"`
	CreatedAt       int64  `json:"created_at"`
	ActualQuota     int    `json:"actual_quota"`
	ExpectedQuota   int    `json:"expected_quota"`
	DifferenceQuota int    `json:"difference_quota"`
}

type TaskBillingAuditReport struct {
	Scanned         int                   `json:"scanned"`
	Affected        int                   `json:"affected"`
	DifferenceQuota int                   `json:"difference_quota"`
	Rows            []TaskBillingAuditRow `json:"rows"`
	DryRun          bool                  `json:"dry_run"`
}

func AuditTaskBilling(limit int) (TaskBillingAuditReport, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rules := task_billing_rules.GetRulesCopy()
	modelNames := make([]string, 0, len(rules))
	for modelName, rule := range rules {
		if rule.Mode == task_billing_rules.ModePerCall {
			modelNames = append(modelNames, modelName)
		}
	}
	report := TaskBillingAuditReport{DryRun: true}
	if len(modelNames) == 0 {
		return report, nil
	}
	var tasks []model.Task
	if err := model.DB.
		Order("id DESC").
		Limit(limit).
		Find(&tasks).Error; err != nil {
		return report, err
	}
	report.Scanned = len(tasks)
	for _, task := range tasks {
		modelName := task.Properties.OriginModelName
		if task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.OriginModelName != "" {
			modelName = task.PrivateData.BillingContext.OriginModelName
		}
		rule, ok := rules[modelName]
		if !ok || rule.Mode != task_billing_rules.ModePerCall {
			continue
		}
		row, affected := auditPerCallTask(task, common.QuotaPerUnit)
		if !affected {
			continue
		}
		report.Rows = append(report.Rows, row)
		report.Affected++
		report.DifferenceQuota += row.DifferenceQuota
	}
	return report, nil
}

func auditPerCallTask(task model.Task, quotaPerUnit float64) (TaskBillingAuditRow, bool) {
	context := task.PrivateData.BillingContext
	if context == nil || context.ModelPrice < 0 || quotaPerUnit <= 0 {
		return TaskBillingAuditRow{}, false
	}
	expected := int(context.ModelPrice * quotaPerUnit * context.GroupRatio)
	if expected == task.Quota {
		return TaskBillingAuditRow{}, false
	}
	modelName := context.OriginModelName
	if modelName == "" {
		modelName = task.Properties.OriginModelName
	}
	return TaskBillingAuditRow{
		TaskID:          task.TaskID,
		UserID:          task.UserId,
		ModelName:       modelName,
		CreatedAt:       task.CreatedAt,
		ActualQuota:     task.Quota,
		ExpectedQuota:   expected,
		DifferenceQuota: task.Quota - expected,
	}, true
}
