package config

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// ConfigManager 统一管理所有配置
type ConfigManager struct {
	configs map[string]any
	mutex   sync.RWMutex
}

var GlobalConfig = NewConfigManager()

func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs: make(map[string]any),
	}
}

// Register 注册一个配置模块
func (cm *ConfigManager) Register(name string, config any) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.configs[name] = config
}

// Get 获取指定配置模块
func (cm *ConfigManager) Get(name string) any {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.configs[name]
}

// Snapshot returns a detached copy held stable while it is read.
func Snapshot[T any](cm *ConfigManager, value *T, clone func(*T) T) T {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return clone(value)
}

// ValidateValue checks the format of an option without changing the live module.
func (cm *ConfigManager) ValidateValue(name, key, value string) error {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	cfg, ok := cm.configs[name]
	if !ok {
		return nil
	}
	shadow := reflect.New(reflect.TypeOf(cfg).Elem())
	return updateConfigFromMap(shadow.Interface(), map[string]string{key: value})
}

// Update applies a validated set of fields atomically to a registered module.
func (cm *ConfigManager) Update(name string, values map[string]string) (bool, error) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cfg, ok := cm.configs[name]
	if !ok {
		return false, nil
	}
	return true, updateConfigFromMap(cfg, values)
}

// LoadFromDB 从数据库加载配置
func (cm *ConfigManager) LoadFromDB(options map[string]string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	for name, config := range cm.configs {
		prefix := name + "."
		configMap := make(map[string]string)

		// 收集属于此配置的所有选项
		for key, value := range options {
			if after, ok := strings.CutPrefix(key, prefix); ok {
				configKey := after
				configMap[configKey] = value
			}
		}

		// 如果找到配置项，则更新配置
		if len(configMap) > 0 {
			if err := updateConfigFromMap(config, configMap); err != nil {
				common.SysError(common.LogText("failed to update config %s: %s", name, err.Error()))
				continue
			}
		}
	}

	return nil
}

// SaveToDB 将配置保存到数据库
func (cm *ConfigManager) SaveToDB(updateFunc func(key, value string) error) error {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	for name, config := range cm.configs {
		configMap, err := configToMap(config)
		if err != nil {
			return err
		}

		for key, value := range configMap {
			dbKey := name + "." + key
			if err := updateFunc(dbKey, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// 辅助函数：将配置对象转换为map
func configToMap(config any) (map[string]string, error) {
	result := make(map[string]string)

	val := reflect.ValueOf(config)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil, nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		// 处理不同类型的字段
		var strValue string
		switch field.Kind() {
		case reflect.String:
			strValue = field.String()
		case reflect.Bool:
			strValue = strconv.FormatBool(field.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			strValue = strconv.FormatInt(field.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			strValue = strconv.FormatUint(field.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			strValue = strconv.FormatFloat(field.Float(), 'f', -1, 64)
		case reflect.Pointer:
			// 处理指针类型：如果非 nil，序列化指向的值
			if !field.IsNil() {
				bytes, err := json.Marshal(field.Interface())
				if err != nil {
					return nil, err
				}
				strValue = string(bytes)
			} else {
				// nil 指针序列化为 "null"
				strValue = "null"
			}
		case reflect.Map, reflect.Slice, reflect.Struct:
			// 复杂类型使用JSON序列化
			bytes, err := json.Marshal(field.Interface())
			if err != nil {
				return nil, err
			}
			strValue = string(bytes)
		default:
			// 跳过不支持的类型
			continue
		}

		result[key] = strValue
	}

	return result, nil
}

// 辅助函数：从map更新配置对象
func updateConfigFromMap(config any, configMap map[string]string) error {
	ptr := reflect.ValueOf(config)
	if ptr.Kind() != reflect.Pointer || ptr.IsNil() || ptr.Elem().Kind() != reflect.Struct {
		return nil
	}
	val := ptr.Elem()
	typ := val.Type()
	// Parse each value into detached storage. A decode error must not mutate a
	// map, slice or pointer (or publish a preceding field from the same batch).
	staged := make(map[int]reflect.Value)
	for i := range val.NumField() {
		fieldType := typ.Field(i)
		if !fieldType.IsExported() {
			continue
		}
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}
		raw, ok := configMap[key]
		if !ok {
			continue
		}
		fresh := reflect.New(fieldType.Type)
		field := fresh.Elem()
		var err error
		switch field.Kind() {
		case reflect.String:
			field.SetString(raw)
		case reflect.Bool:
			var v bool
			v, err = strconv.ParseBool(raw)
			field.SetBool(v)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			var v int64
			v, err = strconv.ParseInt(integralConfigText(raw), 10, field.Type().Bits())
			field.SetInt(v)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			var v uint64
			v, err = strconv.ParseUint(integralConfigText(raw), 10, field.Type().Bits())
			field.SetUint(v)
		case reflect.Float32, reflect.Float64:
			var v float64
			v, err = strconv.ParseFloat(raw, field.Type().Bits())
			if math.IsNaN(v) || math.IsInf(v, 0) {
				err = fmt.Errorf("non-finite float")
			}
			field.SetFloat(v)
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Struct:
			err = json.Unmarshal([]byte(raw), fresh.Interface())
		default:
			continue
		}
		if err != nil {
			// Do not include the underlying parse error: values can be secrets.
			return fmt.Errorf("invalid configuration value for %s", key)
		}
		staged[i] = field
	}
	for i, field := range staged {
		val.Field(i).Set(field)
	}
	return nil
}

func integralConfigText(raw string) string {
	// Legacy database entries may store "2.000000" for an integer. Accept
	// the exact integral spelling without a lossy float conversion.
	if whole, fraction, ok := strings.Cut(raw, "."); ok && fraction != "" && strings.Trim(fraction, "0") == "" {
		return whole
	}
	return raw
}

// ConfigToMap 将配置对象转换为map（导出函数）
func ConfigToMap(config any) (map[string]string, error) {
	return configToMap(config)
}

// UpdateConfigFromMap 从map更新配置对象（导出函数）
func UpdateConfigFromMap(config any, configMap map[string]string) error {
	return updateConfigFromMap(config, configMap)
}

// ExportAllConfigs 导出所有已注册的配置为扁平结构
func (cm *ConfigManager) ExportAllConfigs() map[string]string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	result := make(map[string]string)

	for name, cfg := range cm.configs {
		configMap, err := ConfigToMap(cfg)
		if err != nil {
			continue
		}

		// 使用 "模块名.配置项" 的格式添加到结果中
		for key, value := range configMap {
			result[name+"."+key] = value
		}
	}

	return result
}
