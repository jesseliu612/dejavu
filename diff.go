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
	"github.com/siyuan-note/dejavu/entity"
)

// DiffUpsertRemove 比较 left 多于/变动 right 的文件以及 left 少于 right 的文件。
func (repo *Repo) DiffUpsertRemove(left, right []*entity.File) (upserts, removes []*entity.File) {
	l := map[string]*entity.File{}
	r := map[string]*entity.File{}
	for _, f := range left {
		l[f.Path] = f
	}
	for _, f := range right {
		r[f.Path] = f
	}

	for lPath, lFile := range l {
		rFile := r[lPath]
		if nil == rFile {
			upserts = append(upserts, l[lPath])
			continue
		}
		if lFile.Updated != rFile.Updated || lFile.Path != rFile.Path {
			upserts = append(upserts, l[lPath])
			continue
		}
	}

	for rPath := range r {
		lFile := l[rPath]
		if nil == lFile {
			removes = append(removes, r[rPath])
			continue
		}
	}
	return
}
