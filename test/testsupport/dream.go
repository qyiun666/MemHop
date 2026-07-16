package testsupport

import (
	"github.com/qyiun666/memhop/memhop"
)

// 执行全部dream
func RunDream() (*memhop.DreamReport, error) {
	return RunDreamOn(nil)
}

// 执行指定dream
func RunDreamOn(l2IDs []string) (*memhop.DreamReport, error) {
	mh := OpenMemHop()
	defer mh.Close()
	var opts *memhop.DreamOptions
	if len(l2IDs) > 0 {
		opts = &memhop.DreamOptions{L2IDs: l2IDs}
	}
	return mh.Dream(opts)
}

// 查看L0
func GetProfile() (*memhop.ProfileSlot, error) {
	mh := OpenMemHop()
	defer mh.Close()
	return mh.GetProfile()
}

// 设置L0
func SetProfile(delta memhop.ProfileDelta) error {
	mh := OpenMemHop()
	defer mh.Close()
	return mh.SetProfile(delta)
}

// 列出L2主题
func ListL2Topics(page, pageSize int) (*memhop.TopicListResult, error) {
	mh := OpenMemHop()
	defer mh.Close()
	return mh.ListL2(memhop.TopicListQuery{
		Page:     page,
		PageSize: pageSize,
	})
}

// 获取场景树
func GetSceneTree(sceneID string) (*memhop.SceneTreeResult, error) {
	mh := OpenMemHop()
	defer mh.Close()
	return mh.GetSceneTree(sceneID)
}
