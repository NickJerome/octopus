package op

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

var statsHourlyCache [24]model.StatsHourly
var statsHourlyCacheLock sync.RWMutex

var channelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道 ID。
var channelStatsNeedUpdateLock sync.Mutex           // 保护渠道统计累加和待写集合。

var channelModelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道模型 ID。
var channelModelStatsNeedUpdateLock sync.Mutex           // 保护渠道模型统计累加和待写集合。

var channelKeyStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道凭据 ID。
var channelKeyStatsNeedUpdateLock sync.Mutex           // 保护渠道凭据统计累加和待写集合。

var statsAPIKeyCache = cache.New[int, model.StatsAPIKey](16)
var statsAPIKeyCacheNeedUpdate = make(map[int]struct{})
var statsAPIKeyCacheNeedUpdateLock sync.Mutex

// apiKeyDailyKey 是 API Key 每日统计在缓存与待写集合中的复合主键。
type apiKeyDailyKey struct {
	apiKeyID int
	date     string
}

var statsAPIKeyDailyCache = cache.New[apiKeyDailyKey, model.StatsAPIKeyDaily](16)
var statsAPIKeyDailyNeedUpdate = make(map[apiKeyDailyKey]struct{})
var statsAPIKeyDailyNeedUpdateLock sync.Mutex

// apiKeyModelKey 是 API Key 模型统计在缓存与待写集合中的复合主键。
type apiKeyModelKey struct {
	apiKeyID int
	model    string
}

var statsAPIKeyModelCache = cache.New[apiKeyModelKey, model.StatsAPIKeyModel](16)
var statsAPIKeyModelNeedUpdate = make(map[apiKeyModelKey]struct{})
var statsAPIKeyModelNeedUpdateLock sync.Mutex

func StatsSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log.Debugf("stats save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("stats save db task finished, save time: %s", time.Since(startTime))
	}()
	if err := StatsSaveDB(ctx); err != nil {
		log.Errorf("stats save db error: %v", err)
		return
	}
}

func StatsSaveDB(ctx context.Context) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsDailyCacheLock.RLock()
	dailySnap := statsDailyCache
	statsDailyCacheLock.RUnlock()

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)
	apiKeyDailyKeys := drainDirtySet(&statsAPIKeyDailyNeedUpdateLock, statsAPIKeyDailyNeedUpdate)
	apiKeyModelKeys := drainDirtySet(&statsAPIKeyModelNeedUpdateLock, statsAPIKeyModelNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailySnap, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs, apiKeyDailyKeys, apiKeyModelKeys); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs, apiKeyDailyKeys, apiKeyModelKeys)
		return err
	}
	return nil
}

// drainDirtySet 取出并清空一个待写集合。
func drainDirtySet[K comparable](lock *sync.Mutex, set map[K]struct{}) []K {
	lock.Lock()
	defer lock.Unlock()
	ids := make([]K, 0, len(set))
	for id := range set {
		ids = append(ids, id)
		delete(set, id)
	}
	return ids
}

// restoreDirtySet 把一批主键放回待写集合, 用于持久化失败后重试。
func restoreDirtySet[K comparable](lock *sync.Mutex, set map[K]struct{}, ids []K) {
	lock.Lock()
	defer lock.Unlock()
	for _, id := range ids {
		set[id] = struct{}{}
	}
}

// restoreStatsDirty 在统计持久化失败后恢复本批待写标记。
func restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs []int, apiKeyDailyKeys []apiKeyDailyKey, apiKeyModelKeys []apiKeyModelKey) {
	restoreDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate, channelIDs)
	restoreDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate, modelIDs)
	restoreDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate, keyIDs)
	restoreDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate, apiKeyIDs)
	restoreDirtySet(&statsAPIKeyDailyNeedUpdateLock, statsAPIKeyDailyNeedUpdate, apiKeyDailyKeys)
	restoreDirtySet(&statsAPIKeyModelNeedUpdateLock, statsAPIKeyModelNeedUpdate, apiKeyModelKeys)
}

func persistStatsSnapshots(
	ctx context.Context,
	totalSnap model.StatsTotal,
	dailySnap model.StatsDaily,
	hourlyAll [24]model.StatsHourly,
	channelIDs []int,
	modelIDs []int,
	keyIDs []int,
	apiKeyIDs []int,
	apiKeyDailyKeys []apiKeyDailyKey,
	apiKeyModelKeys []apiKeyModelKey,
) error {
	dbConn := db.GetDB().WithContext(ctx)

	if result := dbConn.Save(&totalSnap); result.Error != nil {
		return result.Error
	}
	if result := dbConn.Save(&dailySnap); result.Error != nil {
		return result.Error
	}

	todayDate := time.Now().Format("20060102")
	hourlyStats := make([]model.StatsHourly, 0, 24)
	for hour := 0; hour < 24; hour++ {
		if hourlyAll[hour].Date == todayDate {
			hourlyStats = append(hourlyStats, hourlyAll[hour])
		}
	}
	if len(hourlyStats) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}},
			UpdateAll: true,
		}).Create(&hourlyStats); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range channelIDs {
		channel, ok := channelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.Channel{}).
			Where("id = ?", channel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range modelIDs {
		channelModel, ok := channelModelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelModel{}).
			Where("id = ?", channelModel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelModel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range keyIDs {
		channelKey, ok := channelKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelKey{}).
			Where("id = ?", channelKey.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelKey); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range apiKeyIDs {
		ak, ok := statsAPIKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Save(&ak); result.Error != nil {
			return result.Error
		}
	}

	for _, key := range apiKeyDailyKeys {
		daily, ok := statsAPIKeyDailyCache.Get(key)
		if !ok {
			continue
		}
		if result := dbConn.Save(&daily); result.Error != nil {
			return result.Error
		}
	}

	for _, key := range apiKeyModelKeys {
		apiKeyModel, ok := statsAPIKeyModelCache.Get(key)
		if !ok {
			continue
		}
		if result := dbConn.Save(&apiKeyModel); result.Error != nil {
			return result.Error
		}
	}

	return nil
}

func statsSaveDBWithDailyOverride(ctx context.Context, dailyOverride model.StatsDaily) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)
	apiKeyDailyKeys := drainDirtySet(&statsAPIKeyDailyNeedUpdateLock, statsAPIKeyDailyNeedUpdate)
	apiKeyModelKeys := drainDirtySet(&statsAPIKeyModelNeedUpdateLock, statsAPIKeyModelNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailyOverride, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs, apiKeyDailyKeys, apiKeyModelKeys); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs, apiKeyDailyKeys, apiKeyModelKeys)
		return err
	}
	return nil
}

func StatsDailyUpdate(ctx context.Context, metrics model.StatsMetrics) error {
	today := time.Now().Format("20060102")

	statsDailyCacheLock.Lock()
	if statsDailyCache.Date == today {
		statsDailyCache.StatsMetrics.Add(metrics)
		statsDailyCacheLock.Unlock()
		return nil
	}

	prevDaily := statsDailyCache
	statsDailyCache = model.StatsDaily{Date: today}
	statsDailyCache.StatsMetrics.Add(metrics)
	statsDailyCacheLock.Unlock()

	return statsSaveDBWithDailyOverride(ctx, prevDaily)
}

func StatsTotalUpdate(metrics model.StatsMetrics) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	if statsTotalCache.ID == 0 {
		statsTotalCache.ID = 1
	}
	statsTotalCache.StatsMetrics.Add(metrics)
	return nil
}

func StatsHourlyUpdate(metrics model.StatsMetrics) error {
	now := time.Now()
	nowHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.Lock()
	defer statsHourlyCacheLock.Unlock()

	if statsHourlyCache[nowHour].Date != todayDate {
		statsHourlyCache[nowHour] = model.StatsHourly{
			Hour: nowHour,
			Date: todayDate,
		}
	}

	statsHourlyCache[nowHour].StatsMetrics.Add(metrics)
	return nil
}

// ChannelModelStatsUpdate 累加渠道模型统计并标记对应模型待持久化。
func ChannelModelStatsUpdate(channelModelID int, metrics model.StatsMetrics) error {
	channelModelStatsNeedUpdateLock.Lock()
	defer channelModelStatsNeedUpdateLock.Unlock()
	channelModel, ok := channelModelCache.Get(channelModelID)
	if !ok {
		return nil
	}
	channelModel.StatsMetrics.Add(metrics)
	channelModelCache.Set(channelModelID, channelModel)
	channelModelStatsNeedUpdate[channelModelID] = struct{}{}
	return nil
}

// ChannelKeyStatsUpdate 累加渠道凭据统计并标记对应凭据待持久化。
func ChannelKeyStatsUpdate(channelKeyID int, metrics model.StatsMetrics) error {
	channelKeyStatsNeedUpdateLock.Lock()
	defer channelKeyStatsNeedUpdateLock.Unlock()
	channelKey, ok := channelKeyCache.Get(channelKeyID)
	if !ok {
		return nil
	}
	channelKey.StatsMetrics.Add(metrics)
	channelKeyCache.Set(channelKeyID, channelKey)
	channelKeyStatsNeedUpdate[channelKeyID] = struct{}{}
	return nil
}

// ChannelStatsUpdate 累加渠道统计并标记对应渠道待持久化。
func ChannelStatsUpdate(channelID int, metrics model.StatsMetrics) error {
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return nil
	}
	channel.StatsMetrics.Add(metrics)
	channelCache.Set(channelID, channel)
	channelStatsNeedUpdate[channelID] = struct{}{}
	return nil
}

func StatsAPIKeyUpdate(apiKeyID int, metrics model.StatsMetrics) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	apiKeyCache, ok := statsAPIKeyCache.Get(apiKeyID)
	if !ok {
		apiKeyCache = model.StatsAPIKey{
			APIKeyID: apiKeyID,
		}
	}
	apiKeyCache.StatsMetrics.Add(metrics)
	statsAPIKeyCache.Set(apiKeyID, apiKeyCache)
	statsAPIKeyCacheNeedUpdate[apiKeyID] = struct{}{}
	return nil
}

// StatsAPIKeyDailyUpdate 累加 API Key 当日统计并标记对应日期待持久化。
func StatsAPIKeyDailyUpdate(apiKeyID int, metrics model.StatsMetrics) error {
	key := apiKeyDailyKey{apiKeyID: apiKeyID, date: time.Now().Format("20060102")}
	statsAPIKeyDailyNeedUpdateLock.Lock()
	defer statsAPIKeyDailyNeedUpdateLock.Unlock()
	daily, ok := statsAPIKeyDailyCache.Get(key)
	if !ok {
		daily = model.StatsAPIKeyDaily{
			APIKeyID: apiKeyID,
			Date:     key.date,
		}
	}
	daily.StatsMetrics.Add(metrics)
	statsAPIKeyDailyCache.Set(key, daily)
	statsAPIKeyDailyNeedUpdate[key] = struct{}{}
	return nil
}

// StatsAPIKeyModelUpdate 累加 API Key 模型统计并标记对应模型待持久化。
func StatsAPIKeyModelUpdate(apiKeyID int, modelName string, metrics model.StatsMetrics) error {
	key := apiKeyModelKey{apiKeyID: apiKeyID, model: modelName}
	statsAPIKeyModelNeedUpdateLock.Lock()
	defer statsAPIKeyModelNeedUpdateLock.Unlock()
	apiKeyModel, ok := statsAPIKeyModelCache.Get(key)
	if !ok {
		apiKeyModel = model.StatsAPIKeyModel{
			APIKeyID: apiKeyID,
			Model:    modelName,
		}
	}
	apiKeyModel.StatsMetrics.Add(metrics)
	statsAPIKeyModelCache.Set(key, apiKeyModel)
	statsAPIKeyModelNeedUpdate[key] = struct{}{}
	return nil
}

func StatsAPIKeyDel(id int) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	if _, ok := statsAPIKeyCache.Get(id); !ok {
		statsAPIKeyCacheNeedUpdateLock.Unlock()
	} else {
		statsAPIKeyCache.Del(id)
		delete(statsAPIKeyCacheNeedUpdate, id)
		statsAPIKeyCacheNeedUpdateLock.Unlock()
	}

	statsAPIKeyDailyNeedUpdateLock.Lock()
	for key := range statsAPIKeyDailyCache.GetAll() {
		if key.apiKeyID == id {
			statsAPIKeyDailyCache.Del(key)
			delete(statsAPIKeyDailyNeedUpdate, key)
		}
	}
	statsAPIKeyDailyNeedUpdateLock.Unlock()

	statsAPIKeyModelNeedUpdateLock.Lock()
	for key := range statsAPIKeyModelCache.GetAll() {
		if key.apiKeyID == id {
			statsAPIKeyModelCache.Del(key)
			delete(statsAPIKeyModelNeedUpdate, key)
		}
	}
	statsAPIKeyModelNeedUpdateLock.Unlock()

	dbConn := db.GetDB()
	if err := dbConn.Where("api_key_id = ?", id).Delete(&model.StatsAPIKeyDaily{}).Error; err != nil {
		return err
	}
	if err := dbConn.Where("api_key_id = ?", id).Delete(&model.StatsAPIKeyModel{}).Error; err != nil {
		return err
	}
	return dbConn.Delete(&model.StatsAPIKey{}, id).Error
}

func StatsTotalGet() model.StatsTotal {
	statsTotalCacheLock.RLock()
	defer statsTotalCacheLock.RUnlock()
	return statsTotalCache
}

func StatsAPIKeyGet(id int) model.StatsAPIKey {
	if stats, ok := statsAPIKeyCache.Get(id); ok {
		return stats
	}
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	stats, ok := statsAPIKeyCache.Get(id)
	if !ok {
		tmp := model.StatsAPIKey{
			APIKeyID: id,
		}
		statsAPIKeyCache.Set(id, tmp)
		statsAPIKeyCacheNeedUpdate[id] = struct{}{}
		return tmp
	}
	return stats
}

func StatsAPIKeyList() []model.StatsAPIKey {
	apiKeys := make([]model.StatsAPIKey, 0, statsAPIKeyCache.Len())
	for _, v := range statsAPIKeyCache.GetAll() {
		apiKeys = append(apiKeys, v)
	}
	return apiKeys
}

// StatsAPIKeyDailyList 返回指定 API Key 自 since 当天(含)起的每日统计, 按日期升序。
// 只取窗口内的数据: 界面上的热力图有固定跨度, 缓存里的更早日期不外发。
func StatsAPIKeyDailyList(apiKeyID int, since string) []model.StatsAPIKeyDaily {
	all := statsAPIKeyDailyCache.GetAll()
	list := make([]model.StatsAPIKeyDaily, 0, len(all))
	for key, daily := range all {
		if key.apiKeyID != apiKeyID || key.date < since {
			continue
		}
		list = append(list, daily)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
	return list
}

// StatsAPIKeyModelList 返回指定 API Key 的全部模型统计, 按模型名称排序保证顺序稳定。
func StatsAPIKeyModelList(apiKeyID int) []model.StatsAPIKeyModel {
	all := statsAPIKeyModelCache.GetAll()
	list := make([]model.StatsAPIKeyModel, 0, len(all))
	for key, apiKeyModel := range all {
		if key.apiKeyID != apiKeyID {
			continue
		}
		list = append(list, apiKeyModel)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Model < list[j].Model })
	return list
}

func StatsHourlyGet() []model.StatsHourly {
	now := time.Now()
	currentHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.RLock()
	defer statsHourlyCacheLock.RUnlock()

	result := make([]model.StatsHourly, 0, currentHour+1)

	for hour := 0; hour <= currentHour; hour++ {
		if statsHourlyCache[hour].Date == todayDate {
			result = append(result, statsHourlyCache[hour])
		} else {
			result = append(result, model.StatsHourly{
				Hour: hour,
				Date: todayDate,
			})
		}
	}

	return result
}

// StatsGetDaily 返回 since 当天及其之后的每日统计, since 为 20060102 格式。
// 只取窗口内的数据: 界面上的热力图与趋势图都有固定跨度, 全量返回会随运行时长无界增长。
func StatsGetDaily(ctx context.Context, since string) ([]model.StatsDaily, error) {
	var statsDaily []model.StatsDaily
	result := db.GetDB().WithContext(ctx).Where("date >= ?", since).Order("date").Find(&statsDaily)
	if result.Error != nil {
		return nil, result.Error
	}
	return statsDaily, nil
}

func statsRefreshCache(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	var loadedDaily model.StatsDaily
	result := dbConn.Last(&loadedDaily)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get daily stats: %v", result.Error)
	}
	if result.RowsAffected == 0 || loadedDaily.Date != today {
		loadedDaily = model.StatsDaily{Date: today}
	}

	var loadedTotal model.StatsTotal
	result = dbConn.First(&loadedTotal)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get total stats: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		loadedTotal = model.StatsTotal{ID: 1}
	} else if loadedTotal.ID == 0 {
		loadedTotal.ID = 1
	}

	var loadedHourly []model.StatsHourly
	result = dbConn.Find(&loadedHourly)
	if result.Error != nil {
		return fmt.Errorf("failed to get hourly stats: %v", result.Error)
	}

	statsDailyCacheLock.Lock()
	statsDailyCache = loadedDaily
	statsDailyCacheLock.Unlock()

	statsTotalCacheLock.Lock()
	statsTotalCache = loadedTotal
	statsTotalCacheLock.Unlock()

	var loadedAPIKeys []model.StatsAPIKey
	result = dbConn.Find(&loadedAPIKeys)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key stats: %v", result.Error)
	}

	statsAPIKeyCache.Clear()
	// 就地清空而非重新赋值: drainDirtySet 与 restoreDirtySet 持有该 map 的引用, 换 map 会让它们写到旧对象上。
	statsAPIKeyCacheNeedUpdateLock.Lock()
	clear(statsAPIKeyCacheNeedUpdate)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeys {
		statsAPIKeyCache.Set(v.APIKeyID, v)
	}

	var loadedAPIKeyDailies []model.StatsAPIKeyDaily
	result = dbConn.Find(&loadedAPIKeyDailies)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key daily stats: %v", result.Error)
	}

	statsAPIKeyDailyCache.Clear()
	statsAPIKeyDailyNeedUpdateLock.Lock()
	clear(statsAPIKeyDailyNeedUpdate)
	statsAPIKeyDailyNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeyDailies {
		statsAPIKeyDailyCache.Set(apiKeyDailyKey{apiKeyID: v.APIKeyID, date: v.Date}, v)
	}

	var loadedAPIKeyModels []model.StatsAPIKeyModel
	result = dbConn.Find(&loadedAPIKeyModels)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key model stats: %v", result.Error)
	}

	statsAPIKeyModelCache.Clear()
	statsAPIKeyModelNeedUpdateLock.Lock()
	clear(statsAPIKeyModelNeedUpdate)
	statsAPIKeyModelNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeyModels {
		statsAPIKeyModelCache.Set(apiKeyModelKey{apiKeyID: v.APIKeyID, model: v.Model}, v)
	}

	statsHourlyCacheLock.Lock()
	statsHourlyCache = [24]model.StatsHourly{}
	for _, v := range loadedHourly {
		if v.Hour >= 0 && v.Hour < 24 {
			statsHourlyCache[v.Hour] = v
		}
	}
	statsHourlyCacheLock.Unlock()

	return nil
}
