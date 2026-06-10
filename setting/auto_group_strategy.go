package setting

import "fmt"

// AutoGroupStrategy 控制 auto 分组候选列表的全局排序策略。
// 该文件为二开新增，上游同步时无冲突。
const (
	// AutoGroupStrategyOrder 按管理员配置的 AutoGroups 顺序选组（默认，与上游行为一致）
	AutoGroupStrategyOrder = "order"
	// AutoGroupStrategyCheapest 按有效倍率（GroupGroupRatio 优先，其次 GroupRatio）从低到高选组
	AutoGroupStrategyCheapest = "cheapest"
)

const AutoGroupStrategyOptionKey = "AutoGroupStrategy"

var autoGroupStrategy = AutoGroupStrategyOrder

func GetAutoGroupStrategy() string {
	return autoGroupStrategy
}

func UpdateAutoGroupStrategy(value string) error {
	switch value {
	case "", AutoGroupStrategyOrder:
		autoGroupStrategy = AutoGroupStrategyOrder
	case AutoGroupStrategyCheapest:
		autoGroupStrategy = AutoGroupStrategyCheapest
	default:
		return fmt.Errorf("invalid auto group strategy: %s", value)
	}
	return nil
}
