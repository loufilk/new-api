package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Keep the monotonic group-version singleton independent of other fixtures.
func TestTokenGroupHealthPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL fixture")
	}
	if os.Getenv("TOKEN_GROUP_HEALTH_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTokenGroupHealthPostgres$", "-test.v", "-test.timeout=2m")
		cmd.Env = append(os.Environ(), "TOKEN_GROUP_HEALTH_TEST_CHILD=1", "TEST_TASK_DB_DIALECT=postgres")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		t.Log(string(output))
		return
	}

	require.NoError(t, i18n.Init())
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.AutoMigrate(&model.PerfMetric{}, &model.UserGroupEntitlement{}))
	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("isolated PostgreSQL server_version=%s", version)

	hour := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).Unix()
	require.NoError(t, db.Create(&[]model.Option{
		{Key: "GroupRatio", Value: `{"public":1,"empty":1,"disabled":1,"reserved":1,"foreign":1,"recycled":1,"renamed":1,"retired":1,"fresh":1}`},
		{Key: "UserUsableGroups", Value: `{"public":"Public","empty":"Empty","disabled":"Disabled","reserved":"Private","recycled":"Recycled","renamed":"Renamed","retired":"Reactivated","fresh":"Fresh"}`},
		{Key: "AutoGroups", Value: `["public"]`},
		{Key: "GroupCatalogMetadata", Value: `{"reserved":{"label":"Private","exclusive":true}}`},
		{Key: "GroupConfigVersion", Value: "1"},
		{Key: "GroupRenameAliases", Value: `{"other-retired-name":"renamed","retired":"foreign"}`},
		{Key: "GroupCatalogMarketActivityCursor", Value: "1"},
		{Key: "GroupCatalogMarketActivity", Value: fmt.Sprintf(`{"recycled":{"first_configured_at":%d},"renamed":{"first_configured_at":%d},"fresh":{"first_configured_at":%d}}`, hour+120, hour-25*3600, hour+120)},
	}).Error)
	require.NoError(t, model.LoadOptionsFromDatabase())

	owner := model.User{Username: "health-owner", AffCode: "health-owner", Group: "public", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	other := model.User{Username: "health-other", AffCode: "health-other", Group: "public", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	ownerSession, err := service.CreateLoginSession(owner.Id, "password", "127.0.0.1", "fixture")
	require.NoError(t, err)
	otherSession, err := service.CreateLoginSession(other.Id, "password", "127.0.0.1", "fixture")
	require.NoError(t, err)

	require.NoError(t, db.Create(&model.UserGroupEntitlement{UserID: owner.Id, GroupName: "reserved"}).Error)
	for group, enabled := range map[string]bool{"public": true, "empty": true, "disabled": false, "reserved": true, "foreign": true, "recycled": true, "renamed": true, "retired": true, "fresh": true} {
		status := common.ChannelStatusEnabled
		if !enabled {
			status = common.ChannelStatusManuallyDisabled
		}
		channel := model.Channel{Name: "fixture-" + group, Group: model.ChannelGroupList(group), Status: status}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: group, Model: "fixture-model", ChannelId: channel.Id, Enabled: true}).Error)
	}

	require.NoError(t, db.Create(&model.GroupRenameOperation{
		RequestID: "health-test-rename-receipt", CreatedAt: hour + 120,
		Result: `{"renames":[{"from":"other-retired-name","to":"renamed"}]}`,
	}).Error)
	require.NoError(t, db.Create(&[]model.PerfMetric{
		{Group: "public", ModelName: "a", BucketTs: hour + 60, RequestCount: 5, SuccessCount: 1, TtftSumMs: 200, TtftCount: 1},
		{Group: "public", ModelName: "b", BucketTs: hour + 300, RequestCount: 7, SuccessCount: 6, TtftSumMs: 100, TtftCount: 2},
		{Group: "public", ModelName: "a", BucketTs: hour - 3600, RequestCount: 4, SuccessCount: 0},
		{Group: "public", ModelName: "a", BucketTs: hour - 24*3600, RequestCount: 100, SuccessCount: 100, TtftSumMs: 10000, TtftCount: 100},
		{Group: "foreign", ModelName: "a", BucketTs: hour, RequestCount: 800, SuccessCount: 800, TtftSumMs: 800, TtftCount: 800},
		{Group: "reserved", ModelName: "a", BucketTs: hour, RequestCount: 3, SuccessCount: 2, TtftSumMs: 200, TtftCount: 2},
		{Group: "disabled", ModelName: "a", BucketTs: hour, RequestCount: 1, SuccessCount: 0, TtftSumMs: 0, TtftCount: 1},
		{Group: "recycled", ModelName: "retired-observation", BucketTs: hour - 3600, RequestCount: 50, SuccessCount: 45},
		{Group: "recycled", ModelName: "overlapping-observation", BucketTs: hour + 300, RequestCount: 50, SuccessCount: 50},
		{Group: "recycled", ModelName: "new-observation", BucketTs: hour + 3600, RequestCount: 2, SuccessCount: 1},
		{Group: "renamed", ModelName: "old-target-observation", BucketTs: hour - 3600, RequestCount: 60, SuccessCount: 60},
		{Group: "renamed", ModelName: "overlapping-observation", BucketTs: hour + 300, RequestCount: 90, SuccessCount: 90},
		{Group: "renamed", ModelName: "current-observation", BucketTs: hour + 3600, RequestCount: 3, SuccessCount: 1},
		{Group: "retired", ModelName: "would-mix", BucketTs: hour + 3600, RequestCount: 4, SuccessCount: 2},
		{Group: "fresh", ModelName: "first-hour-observation", BucketTs: hour + 300, RequestCount: 5, SuccessCount: 1},
		{Group: "fresh", ModelName: "current-observation", BucketTs: hour + 3600, RequestCount: 2, SuccessCount: 1},
	}).Error)

	engine := gin.New()
	engine.GET("/api/token/group-health", middleware.DisableCache(), middleware.TokenManagementAuth(), GetTokenGroupHealth)
	do := func(token, group string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/token/group-health?group="+url.QueryEscape(group), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		require.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
		require.Contains(t, recorder.Header().Get("Cache-Control"), "private")
		return recorder
	}
	decode := func(out *httptest.ResponseRecorder) groupHealthResponse {
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		var result struct {
			Success bool                `json:"success"`
			Data    groupHealthResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(out.Body.Bytes(), &result))
		require.True(t, result.Success)
		require.Len(t, result.Data.Buckets, 24)
		return result.Data
	}

	assert.Equal(t, http.StatusUnauthorized, do("", "public").Code)
	assert.Equal(t, http.StatusBadRequest, do(ownerSession.AccessToken, "auto").Code)
	unknown := do(ownerSession.AccessToken, "nonexistent")
	denied := do(ownerSession.AccessToken, "foreign")
	deniedPrivate := do(otherSession.AccessToken, "reserved")
	assert.Equal(t, http.StatusForbidden, unknown.Code)
	assert.Equal(t, unknown.Body.String(), denied.Body.String())
	assert.Equal(t, unknown.Body.String(), deniedPrivate.Body.String())
	assert.Equal(t, http.StatusForbidden, do(ownerSession.AccessToken, " PUBLIC ").Code)

	public := decode(do(ownerSession.AccessToken, "public"))
	assert.True(t, public.Available)
	assert.True(t, public.MetricsAvailable)
	assert.Equal(t, public.ObservedAt, public.WindowEnd)
	assert.EqualValues(t, 0, public.WindowStart%3600)
	assert.EqualValues(t, 3600, public.BucketSeconds)
	assert.EqualValues(t, 16, public.RequestCount)
	assert.EqualValues(t, 7, public.SuccessCount)
	assert.EqualValues(t, 3, public.TtftCount)
	require.NotNil(t, public.SuccessRate)
	require.NotNil(t, public.AverageTtftMs)
	require.NotNil(t, public.LatestBucketStart)
	assert.EqualValues(t, 43.75, *public.SuccessRate)
	assert.EqualValues(t, 100, *public.AverageTtftMs)
	assert.EqualValues(t, hour, *public.LatestBucketStart)
	assert.EqualValues(t, 12, public.Buckets[22].RequestCount)
	require.NotNil(t, public.Buckets[21].SuccessRate)
	assert.Zero(t, *public.Buckets[21].SuccessRate)
	assert.Nil(t, public.Buckets[21].AverageTtftMs)
	assert.Nil(t, public.Buckets[23].SuccessRate)

	empty := decode(do(ownerSession.AccessToken, "empty"))
	assert.True(t, empty.Available)
	assert.True(t, empty.MetricsAvailable)
	assert.Nil(t, empty.SuccessRate)
	assert.Nil(t, empty.AverageTtftMs)
	assert.Nil(t, empty.LatestBucketStart)

	disabled := decode(do(ownerSession.AccessToken, "disabled"))
	assert.False(t, disabled.Available)
	require.NotNil(t, disabled.SuccessRate)
	require.NotNil(t, disabled.AverageTtftMs)
	assert.Zero(t, *disabled.SuccessRate)
	assert.Zero(t, *disabled.AverageTtftMs)

	private := decode(do(ownerSession.AccessToken, "reserved"))
	assert.EqualValues(t, 3, private.RequestCount)
	// A prior populated full hour or durable retired alias proves reuse;
	// no later bucket can prove that it excludes a slow old stream.
	for _, name := range []string{"recycled", "renamed", "retired"} {
		data := decode(do(ownerSession.AccessToken, name))
		assert.True(t, data.Available)
		assert.False(t, data.MetricsAvailable)
		assert.Zero(t, data.RequestCount, name)
		assert.Nil(t, data.LatestBucketStart)
	}
	fresh := decode(do(ownerSession.AccessToken, "fresh"))
	assert.True(t, fresh.Available)
	assert.True(t, fresh.MetricsAvailable)
	assert.EqualValues(t, 2, fresh.RequestCount)
	assert.Zero(t, fresh.Buckets[22].RequestCount)
	require.NotNil(t, fresh.LatestBucketStart)
	assert.Equal(t, hour+3600, *fresh.LatestBucketStart)

	// Check that a grant lost during the read suppresses already fetched
	// measurements. The hook is confined to this synthetic PG fixture.
	var afterReadErr error
	readCount := 0
	require.NoError(t, db.Callback().Row().After("gorm:row").Register("fixture:group-health-revalidation", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "perf_metrics") {
			readCount++
			afterReadErr = db.Where("user_id = ? AND group_name = ?", owner.Id, "reserved").Delete(&model.UserGroupEntitlement{}).Error
		}
	}))
	revoked := do(ownerSession.AccessToken, "reserved")
	require.NoError(t, afterReadErr)
	assert.Equal(t, 1, readCount, "only one bounded aggregate query reaches the store")
	assert.Equal(t, http.StatusForbidden, revoked.Code)
	assert.Equal(t, unknown.Body.String(), revoked.Body.String())
	assert.Equal(t, http.StatusForbidden, do(ownerSession.AccessToken, "reserved").Code)
	assert.Equal(t, 1, readCount, "an unauthorized group never queries metrics")

	// A stale market generation cannot associate persisted name-keyed data
	// with a current identity. Availability is still served without metrics.
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "GroupCatalogMarketActivityCursor").Update("value", "2").Error)
	require.NoError(t, model.LoadOptionsFromDatabase())
	stale := decode(do(ownerSession.AccessToken, "public"))
	assert.True(t, stale.Available)
	assert.False(t, stale.MetricsAvailable)
	assert.Zero(t, stale.RequestCount)
	assert.Nil(t, stale.LatestBucketStart)
	assert.Equal(t, 1, readCount, "ambiguous lineage bypasses perf_metrics")
}
