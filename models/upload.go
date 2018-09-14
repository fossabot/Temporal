package models

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/RTradeLtd/Temporal/utils"
	"github.com/jinzhu/gorm"
	"github.com/lib/pq"
)

// Upload is our model and database table for all uploads into temporal
type Upload struct {
	gorm.Model
	Hash               string `gorm:"type:varchar(255);not null;"`
	Type               string `gorm:"type:varchar(255);not null;"` //  file, pin
	Name               string `gorm:"type:varchar(255)"`
	NetworkName        string `gorm:"type:varchar(255)"`
	HoldTimeInMonths   int64  `gorm:"type:integer;not null;"`
	UserName           string `gorm:"type:varchar(255);not null;"`
	GarbageCollectDate time.Time
	UserNames          pq.StringArray `gorm:"type:text[];not null;"`
}

const dev = true

// UploadManager is our wrapper used to manipulate the uploads table
type UploadManager struct {
	DB *gorm.DB
}

// NewUploadManager is used to generate an upload manager interface
func NewUploadManager(db *gorm.DB) *UploadManager {
	return &UploadManager{DB: db}
}

// NewUpload is used to create a new upload in the database
func (um *UploadManager) NewUpload(contentHash, uploadType, networkName, username, name string, holdTimeInMonths int64) (*Upload, error) {
	_, err := um.FindUploadByHashAndNetworkAndUser(contentHash, networkName, username)
	if err == nil {
		// this means that there is already an upload in hte database matching this content hash and network name, so we will skip
		return nil, errors.New("attempting to create new upload entry when one already exists in database")
	}
	holdInt, err := strconv.Atoi(fmt.Sprintf("%+v", holdTimeInMonths))
	if err != nil {
		return nil, err
	}
	upload := Upload{
		Hash:               contentHash,
		Type:               uploadType,
		Name:               name,
		NetworkName:        networkName,
		HoldTimeInMonths:   holdTimeInMonths,
		UserName:           username,
		GarbageCollectDate: utils.CalculateGarbageCollectDate(holdInt),
		UserNames:          []string{username},
	}
	if check := um.DB.Create(&upload); check.Error != nil {
		return nil, check.Error
	}
	return &upload, nil
}

// UpdateUpload is used to upadte an already existing upload
func (um *UploadManager) UpdateUpload(holdTimeInMonths int64, username, contentHash, networkName string) (*Upload, error) {
	upload, err := um.FindUploadByHashAndNetworkAndUser(contentHash, networkName, username)
	if err != nil {
		return nil, err
	}
	isUploader := false
	upload.UserName = username
	for _, v := range upload.UserNames {
		if username == v {
			isUploader = true
			break
		}
	}
	if !isUploader {
		upload.UserNames = append(upload.UserNames, username)
	}
	holdInt, err := strconv.Atoi(fmt.Sprintf("%v", holdTimeInMonths))
	if err != nil {
		return nil, err
	}
	oldGcd := upload.GarbageCollectDate
	newGcd := utils.CalculateGarbageCollectDate(holdInt)
	if newGcd.Unix() > oldGcd.Unix() {
		upload.HoldTimeInMonths = holdTimeInMonths
		upload.GarbageCollectDate = oldGcd
	}
	if check := um.DB.Save(upload); check.Error != nil {
		return nil, err
	}
	return upload, nil
}

// RunDatabaseGarbageCollection is used to parse through the database
// and delete all objects whose GCD has passed
// TODO: Maybe move this to the database file?
func (um *UploadManager) RunDatabaseGarbageCollection() (*[]Upload, error) {
	var uploads []Upload
	var deletedUploads []Upload

	if check := um.DB.Find(&uploads); check.Error != nil {
		return nil, check.Error
	}
	for _, v := range uploads {
		if time.Now().Unix() > v.GarbageCollectDate.Unix() {
			if check := um.DB.Delete(&v); check.Error != nil {
				return nil, check.Error
			}
			deletedUploads = append(deletedUploads, v)
		}
	}
	return &deletedUploads, nil
}

// RunTestDatabaseGarbageCollection is used to run a test garbage collection run.
// NOTE that this will delete literally every single object it detects.
func (um *UploadManager) RunTestDatabaseGarbageCollection() (*[]Upload, error) {
	var foundUploads []Upload
	var deletedUploads []Upload
	if !dev {
		return nil, errors.New("not in dev mode")
	}
	// get all uploads
	if check := um.DB.Find(&foundUploads); check.Error != nil {
		return nil, check.Error
	}
	for _, v := range foundUploads {
		if check := um.DB.Delete(v); check.Error != nil {
			return nil, check.Error
		}
		deletedUploads = append(deletedUploads, v)
	}
	return &deletedUploads, nil
}

// FindUploadsByNetwork is used to retrieve all uploads for a given network
func (um *UploadManager) FindUploadsByNetwork(networkName string) (*[]Upload, error) {
	uploads := &[]Upload{}
	if check := um.DB.Where("network_name = ?", networkName).Find(uploads); check.Error != nil {
		return nil, check.Error
	}
	return uploads, nil
}

// FindUploadByHashAndNetworkAndUser is used to find an upload based on its hash, network name, and user who uploaded
func (um *UploadManager) FindUploadByHashAndNetworkAndUser(hash, networkName, username string) (*Upload, error) {
	upload := &Upload{}
	if check := um.DB.Where("hash = ? AND network_name = ? AND user_name = ?", hash, networkName, username).First(upload); check.Error != nil {
		return nil, check.Error
	}
	return upload, nil
}

// FindUploadsByHash is used to return all instances of uploads matching the
// given hash
func (um *UploadManager) FindUploadsByHash(hash string) *[]Upload {

	uploads := []Upload{}

	um.DB.Find(&uploads).Where("hash = ?", hash)

	return &uploads
}

// GetUploadByHashForUser is used to retrieve the last (most recent) upload for a user
func (um *UploadManager) GetUploadByHashForUser(hash string, username string) []*Upload {
	var uploads []*Upload
	um.DB.Find(&uploads).Where("hash = ? AND user_name = ?", hash, username)
	return uploads
}

// GetUploads is used to return all  uploads
func (um *UploadManager) GetUploads() (*[]Upload, error) {
	uploads := []Upload{}
	if check := um.DB.Find(&uploads); check.Error != nil {
		return nil, check.Error
	}
	return &uploads, nil
}

// GetUploadsForUser is used to retrieve all uploads by a user name
func (um *UploadManager) GetUploadsForUser(username string) (*[]Upload, error) {
	uploads := []Upload{}
	if check := um.DB.Where("user_name = ?", username).Find(&uploads); check.Error != nil {
		return nil, check.Error
	}
	return &uploads, nil
}
