// DejaVu - Data snapshot and sync.
// Copyright (c) 2022-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package dejavu

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/88250/gulu"
	"github.com/siyuan-note/dejavu/entity"
	"github.com/siyuan-note/httpclient"
	"github.com/siyuan-note/logging"
)

func (repo *Repo) DownloadTagIndex(tag, id string, cloudInfo *CloudInfo, context map[string]interface{}) (downloadFileCount, downloadChunkCount int, downloadBytes int64, err error) {
	lock.Lock()
	defer lock.Unlock()

	// 从云端下载标签指向的索引
	length, index, err := repo.downloadCloudIndex(id, cloudInfo, context)
	if nil != err {
		logging.LogErrorf("download cloud index failed: %s", err)
		return
	}
	downloadFileCount++
	downloadBytes += length

	// 计算本地缺失的文件
	fetchFileIDs, err := repo.localNotFoundFiles(index.Files)
	if nil != err {
		logging.LogErrorf("get local not found files failed: %s", err)
		return
	}

	// 从云端下载缺失文件并入库
	length, fetchedFiles, err := repo.downloadCloudFilesPut(fetchFileIDs, cloudInfo, context)
	if nil != err {
		logging.LogErrorf("download cloud files put failed: %s", err)
		return
	}
	downloadBytes += length
	downloadFileCount = len(fetchFileIDs)

	// 从文件列表中得到去重后的分块列表
	cloudChunkIDs := repo.getChunks(fetchedFiles)

	// 计算本地缺失的分块
	fetchChunkIDs, err := repo.localNotFoundChunks(cloudChunkIDs)
	if nil != err {
		logging.LogErrorf("get local not found chunks failed: %s", err)
		return
	}

	// 从云端获取分块并入库
	length, err = repo.downloadCloudChunksPut(fetchChunkIDs, cloudInfo, context)
	downloadBytes += length
	downloadChunkCount = len(fetchChunkIDs)

	// 更新本地索引
	err = repo.store.PutIndex(index)
	if nil != err {
		logging.LogErrorf("put index failed: %s", err)
		return
	}

	// 更新本地标签
	err = repo.AddTag(id, tag)
	if nil != err {
		logging.LogErrorf("add tag failed: %s", err)
		return
	}

	// 统计流量
	go repo.addTraffic(0, downloadBytes, cloudInfo)
	return
}

func (repo *Repo) UploadTagIndex(tag, id string, cloudInfo *CloudInfo, context map[string]interface{}) (uploadFileCount, uploadChunkCount int, uploadBytes int64, err error) {
	lock.Lock()
	defer lock.Unlock()

	uploadFileCount, uploadChunkCount, uploadBytes, err = repo.uploadTagIndex(tag, id, cloudInfo, context)
	if e, ok := err.(*os.PathError); ok && os.IsNotExist(err) {
		p := e.Path
		if !strings.Contains(p, "objects") {
			return
		}

		// 索引时正常，但是上传时可能因为外部变更导致对象（文件或者分块）不存在，此时需要告知用户数据仓库已经损坏，需要重置数据仓库
		logging.LogErrorf("upload tag index failed: %s", err)
		err = ErrRepoFatalErr
	}
	return
}

func (repo *Repo) uploadTagIndex(tag, id string, cloudInfo *CloudInfo, context map[string]interface{}) (uploadFileCount, uploadChunkCount int, uploadBytes int64, err error) {
	index, err := repo.store.GetIndex(id)
	if nil != err {
		logging.LogErrorf("get index failed: %s", err)
		return
	}

	if !cloudInfo.CustomSync && cloudInfo.LimitSize <= index.Size {
		err = ErrCloudStorageSizeExceeded
		return
	}

	// 获取云端数据仓库统计信息
	cloudRepoSize, cloudBackupCount, err := repo.getCloudRepoStat(cloudInfo)
	if nil != err {
		logging.LogErrorf("get cloud repo stat failed: %s", err)
		return
	}
	if 12 <= cloudBackupCount {
		err = ErrCloudBackupCountExceeded
		return
	}

	if !cloudInfo.CustomSync && cloudInfo.LimitSize <= cloudRepoSize+index.Size {
		err = ErrCloudStorageSizeExceeded
		return
	}

	// 从云端获取文件列表
	cloudFileIDs, err := repo.getCloudRepoRefsFiles(cloudInfo)
	if nil != err {
		logging.LogErrorf("get cloud repo refs files failed: %s", err)
		return
	}

	// 计算云端缺失的文件
	var uploadFiles []*entity.File
	for _, localFileID := range index.Files {
		if !gulu.Str.Contains(localFileID, cloudFileIDs) {
			var uploadFile *entity.File
			uploadFile, err = repo.store.GetFile(localFileID)
			if nil != err {
				logging.LogErrorf("get file failed: %s", err)
				return
			}
			uploadFiles = append(uploadFiles, uploadFile)
		}
	}

	// 从文件列表中得到去重后的分块列表
	uploadChunkIDs := repo.getChunks(uploadFiles)

	// 计算云端缺失的分块
	uploadChunkIDs, err = repo.getCloudRepoUploadChunks(uploadChunkIDs, cloudInfo)
	if nil != err {
		logging.LogErrorf("get cloud repo upload chunks failed: %s", err)
		return
	}

	// 获取上传凭证
	latestKey := path.Join("siyuan", cloudInfo.UserID, "repo", cloudInfo.Dir, "refs/tags/"+tag)
	keyUploadToken, scopeUploadToken, err := repo.requestScopeKeyUploadToken(latestKey, cloudInfo)
	if nil != err {
		logging.LogErrorf("request upload token failed: %s", err)
		return
	}

	// 上传分块
	length, err := repo.uploadChunks(uploadChunkIDs, scopeUploadToken, cloudInfo, context)
	if nil != err {
		logging.LogErrorf("upload chunks failed: %s", err)
		return
	}
	uploadChunkCount = len(uploadChunkIDs)
	uploadBytes += length

	// 上传文件
	length, err = repo.uploadFiles(uploadFiles, scopeUploadToken, cloudInfo, context)
	if nil != err {
		logging.LogErrorf("upload files failed: %s", err)
		return
	}
	uploadFileCount = len(uploadFiles)
	uploadBytes += length

	// 上传索引
	length, err = repo.uploadIndexes([]*entity.Index{index}, scopeUploadToken, cloudInfo, context)
	uploadFileCount++
	uploadBytes += length

	// 上传标签
	length, err = repo.updateCloudRef("refs/tags/"+tag, keyUploadToken, cloudInfo, context)
	uploadFileCount++
	uploadBytes += length

	// 统计流量
	go repo.addTraffic(uploadBytes, 0, cloudInfo)
	return
}

func (repo *Repo) getCloudRepoUploadChunks(uploadChunkIDs []string, cloudInfo *CloudInfo) (chunks []string, err error) {
	if 1 > len(uploadChunkIDs) {
		return
	}

	result := gulu.Ret.NewResult()
	request := httpclient.NewCloudFileRequest2m()
	resp, err := request.
		SetResult(&result).
		SetBody(map[string]interface{}{"repo": cloudInfo.Dir, "token": cloudInfo.Token, "chunks": uploadChunkIDs}).
		Post(cloudInfo.Server + "/apis/siyuan/dejavu/getRepoUploadChunks?uid=" + cloudInfo.UserID)
	if nil != err {
		return
	}

	if 200 != resp.StatusCode {
		if 401 == resp.StatusCode {
			err = ErrCloudAuthFailed
			return
		}
		err = fmt.Errorf("get cloud repo refs chunks failed [%d]", resp.StatusCode)
		return
	}

	if 0 != result.Code {
		err = fmt.Errorf("get cloud repo refs chunks failed: %s", result.Msg)
		return
	}

	retData := result.Data.(map[string]interface{})
	retChunks := retData["chunks"].([]interface{})
	for _, retChunk := range retChunks {
		chunks = append(chunks, retChunk.(string))
	}
	return
}

func (repo *Repo) getCloudRepoStat(cloudInfo *CloudInfo) (repoSize int64, backupCount int, err error) {
	repoStat, err := repo.GetCloudRepoStat(cloudInfo)
	if nil != err {
		return
	}

	syncSize := int64(repoStat["sync"].(map[string]interface{})["size"].(float64))
	backupSize := int64(repoStat["backup"].(map[string]interface{})["size"].(float64))
	repoSize = syncSize + backupSize
	backupCount = int(repoStat["backup"].(map[string]interface{})["count"].(float64))
	return
}

func (repo *Repo) GetCloudRepoStat(cloudInfo *CloudInfo) (ret map[string]interface{}, err error) {
	result := gulu.Ret.NewResult()
	request := httpclient.NewCloudFileRequest15s()
	resp, err := request.
		SetResult(&result).
		SetBody(map[string]string{"repo": cloudInfo.Dir, "token": cloudInfo.Token}).
		Post(cloudInfo.Server + "/apis/siyuan/dejavu/getRepoStat?uid=" + cloudInfo.UserID)
	if nil != err {
		err = fmt.Errorf("get cloud repo stat failed: %s", err)
		return
	}

	if 200 != resp.StatusCode {
		if 401 == resp.StatusCode {
			err = ErrCloudAuthFailed
			return
		}
		err = fmt.Errorf("get cloud repo stat failed [%d]", resp.StatusCode)
		return
	}

	if 0 != result.Code {
		err = fmt.Errorf("get cloud repo stat failed: %s", result.Msg)
		return
	}

	ret = result.Data.(map[string]interface{})
	return
}

func (repo *Repo) getCloudRepoRefsFiles(cloudInfo *CloudInfo) (files []string, err error) {
	result := gulu.Ret.NewResult()
	request := httpclient.NewCloudFileRequest15s()
	resp, err := request.
		SetResult(&result).
		SetBody(map[string]string{"repo": cloudInfo.Dir, "token": cloudInfo.Token}).
		Post(cloudInfo.Server + "/apis/siyuan/dejavu/getRepoRefsFiles?uid=" + cloudInfo.UserID)
	if nil != err {
		err = fmt.Errorf("get cloud repo refs files failed: %s", err)
		return
	}

	if 200 != resp.StatusCode {
		if 401 == resp.StatusCode {
			err = ErrCloudAuthFailed
			return
		}
		err = fmt.Errorf("get cloud repo refs files failed [%d]", resp.StatusCode)
		return
	}

	if 0 != result.Code {
		err = fmt.Errorf("get cloud repo refs files failed: %s", result.Msg)
		return
	}

	retData := result.Data.(map[string]interface{})
	retFiles := retData["files"].([]interface{})
	for _, retFile := range retFiles {
		files = append(files, retFile.(string))
	}
	return
}

func (repo *Repo) GetCloudRepoTags(cloudInfo *CloudInfo) (tags []map[string]interface{}, err error) {
	result := gulu.Ret.NewResult()
	request := httpclient.NewCloudRequest()
	resp, err := request.
		SetResult(&result).
		SetBody(map[string]string{"repo": cloudInfo.Dir, "token": cloudInfo.Token}).
		Post(cloudInfo.Server + "/apis/siyuan/dejavu/getRepoTags?uid=" + cloudInfo.UserID)
	if nil != err {
		err = fmt.Errorf("get cloud repo tags failed: %s", err)
		return
	}

	if 200 != resp.StatusCode {
		if 401 == resp.StatusCode {
			err = ErrCloudAuthFailed
			return
		}
		err = fmt.Errorf("get cloud repo tags failed [%d]", resp.StatusCode)
		return
	}

	if 0 != result.Code {
		err = fmt.Errorf("get cloud repo tags failed: %s", result.Msg)
		return
	}

	retData := result.Data.(map[string]interface{})
	retTags := retData["tags"].([]interface{})
	for _, retTag := range retTags {
		tags = append(tags, retTag.(map[string]interface{}))
	}
	return
}
