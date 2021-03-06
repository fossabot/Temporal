package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/RTradeLtd/Temporal/mini"
	"github.com/RTradeLtd/Temporal/queue"
	"github.com/RTradeLtd/Temporal/rtfs"
	gocid "github.com/ipfs/go-cid"
	minio "github.com/minio/minio-go"
	log "github.com/sirupsen/logrus"

	"github.com/RTradeLtd/Temporal/models"

	"github.com/RTradeLtd/Temporal/utils"
	"github.com/gin-gonic/gin"
)

// PinToHostedIPFSNetwork is used to pin content to a private ipfs network
func (api *API) pinToHostedIPFSNetwork(c *gin.Context) {
	username := GetAuthenticatedUserFromContext(c)
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB)
	if err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}
	hash := c.Param("hash")
	if _, err := gocid.Decode(hash); err != nil {
		FailOnError(c, err)
		return
	}
	holdTimeInMonths, exists := c.GetPostForm("hold_time")
	if !exists {
		FailNoExistPostForm(c, "hold_time")
		return
	}
	holdTimeInt, err := strconv.ParseInt(holdTimeInMonths, 10, 64)
	if err != nil {
		FailOnError(c, err)
		return
	}

	ip := queue.IPFSPin{
		CID:              hash,
		NetworkName:      networkName,
		UserName:         username,
		HoldTimeInMonths: holdTimeInt,
	}

	mqConnectionURL := api.TConfig.RabbitMQ.URL

	qm, err := queue.Initialize(queue.IpfsPinQueue, mqConnectionURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnServerError(c, err)
		return
	}

	if err = qm.PublishMessageWithExchange(ip, queue.PinExchange); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnServerError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("ipfs pin request for private network sent to backend")

	Respond(c, http.StatusOK, gin.H{"response": "content pin request sent to backend"})
}

// GetFileSizeInBytesForObjectForHostedIPFSNetwork is used to get file size for an object from a private ipfs network
func (api *API) getFileSizeInBytesForObjectForHostedIPFSNetwork(c *gin.Context) {
	username := GetAuthenticatedUserFromContext(c)
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}
	if err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	key := c.Param("key")
	if _, err := gocid.Decode(key); err != nil {
		FailOnError(c, err)
		return
	}
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	sizeInBytes, err := manager.GetObjectFileSizeInBytes(key)
	if err != nil {
		api.LogError(err, IPFSObjectStatError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("private ipfs object file size requested")

	Respond(c, http.StatusOK, gin.H{"response": gin.H{"object": key, "size_in_bytes": sizeInBytes}})

}

// AddFileToHostedIPFSNetworkAdvanced is used to add a file to a private ipfs network in a more advanced and resilient manner
func (api *API) addFileToHostedIPFSNetworkAdvanced(c *gin.Context) {

	username := GetAuthenticatedUserFromContext(c)

	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	if err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	holdTimeInMonths, exists := c.GetPostForm("hold_time")
	if !exists {
		FailNoExistPostForm(c, "hold_time")
		return
	}

	accessKey := api.TConfig.MINIO.AccessKey
	secretKey := api.TConfig.MINIO.SecretKey
	endpoint := fmt.Sprintf("%s:%s", api.TConfig.MINIO.Connection.IP, api.TConfig.MINIO.Connection.Port)

	mqURL := api.TConfig.RabbitMQ.URL

	miniManager, err := mini.NewMinioManager(endpoint, accessKey, secretKey, false)
	if err != nil {
		api.LogError(err, MinioConnectionError)
		FailOnError(c, err)
		return
	}
	fileHandler, err := c.FormFile("file")
	if err != nil {
		// user error, do not log
		FailOnError(c, err)
		return
	}
	if err := api.FileSizeCheck(fileHandler.Size); err != nil {
		FailOnError(c, err)
		return
	}
	fmt.Println("opening file")
	openFile, err := fileHandler.Open()
	if err != nil {
		api.LogError(err, FileOpenError)
		FailOnError(c, err)
		return
	}
	fmt.Println("file opened")
	randUtils := utils.GenerateRandomUtils()
	randString := randUtils.GenerateString(32, utils.LetterBytes)
	objectName := fmt.Sprintf("%s%s", username, randString)
	fmt.Println("storing file in minio")
	if _, err = miniManager.PutObject(FilesUploadBucket, objectName, openFile, fileHandler.Size, minio.PutObjectOptions{}); err != nil {
		api.LogError(err, MinioPutError)
		FailOnError(c, err)
		return
	}
	fmt.Println("file stored in minio")
	ifp := queue.IPFSFile{
		BucketName:       FilesUploadBucket,
		ObjectName:       objectName,
		UserName:         username,
		NetworkName:      networkName,
		HoldTimeInMonths: holdTimeInMonths,
	}
	qm, err := queue.Initialize(queue.IpfsFileQueue, mqURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnError(c, err)
		return
	}
	// we don't use an exchange for file publishes so that rabbitmq distributes round robin
	if err = qm.PublishMessage(ifp); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("advanced private ipfs file upload requested")

	Respond(c, http.StatusOK, gin.H{"response": "file upload request sent to backend"})
}

// AddFileToHostedIPFSNetwork is used to add a file to a private IPFS network via the simple method
func (api *API) addFileToHostedIPFSNetwork(c *gin.Context) {
	username := GetAuthenticatedUserFromContext(c)

	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	if err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	mqURL := api.TConfig.RabbitMQ.URL

	holdTimeinMonths, exists := c.GetPostForm("hold_time")
	if !exists {
		FailNoExistPostForm(c, "hold_time")
		return
	}
	holdTimeInt, err := strconv.ParseInt(holdTimeinMonths, 10, 64)
	if err != nil {
		FailOnError(c, err)
		return
	}
	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}

	ipfsManager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	qm, err := queue.Initialize(queue.DatabaseFileAddQueue, mqURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnError(c, err)
		return
	}

	fmt.Println("fetching file")
	// fetch the file, and create a handler to interact with it
	fileHandler, err := c.FormFile("file")
	if err != nil {
		// user error, do not log
		FailOnError(c, err)
		return
	}
	if err := api.FileSizeCheck(fileHandler.Size); err != nil {
		FailOnError(c, err)
		return
	}
	file, err := fileHandler.Open()
	if err != nil {
		api.LogError(err, FileOpenError)
		FailOnError(c, err)
		return
	}
	resp, err := ipfsManager.Add(file)
	if err != nil {
		api.LogError(err, IPFSAddError)
		FailOnError(c, err)
		return
	}
	fmt.Println("file uploaded")
	dfa := queue.DatabaseFileAdd{
		Hash:             resp,
		HoldTimeInMonths: holdTimeInt,
		UserName:         username,
		NetworkName:      networkName,
	}
	if err = qm.PublishMessage(dfa); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnError(c, err)
		return
	}

	pin := queue.IPFSPin{
		CID:              resp,
		NetworkName:      networkName,
		UserName:         username,
		HoldTimeInMonths: holdTimeInt,
	}

	qm, err = queue.Initialize(queue.IpfsPinQueue, mqURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnError(c, err)
		return
	}
	if err = qm.PublishMessageWithExchange(pin, queue.PinExchange); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("simple private ipfs file upload processed")

	Respond(c, http.StatusOK, gin.H{"response": resp})
}

// IpfsPubSubPublishToHostedIPFSNetwork is used to publish a pubsub message to a private ipfs network
func (api *API) ipfsPubSubPublishToHostedIPFSNetwork(c *gin.Context) {
	username := GetAuthenticatedUserFromContext(c)
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}
	if err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	topic := c.Param("topic")
	message, present := c.GetPostForm("message")
	if !present {
		FailNoExistPostForm(c, "message")
		return
	}
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	if err = manager.PublishPubSubMessage(topic, message); err != nil {
		api.LogError(err, IPFSPubSubPublishError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("private ipfs pub sub message published")

	Respond(c, http.StatusOK, gin.H{"response": gin.H{"topic": topic, "message": message}})
}

// RemovePinFromLocalHostForHostedIPFSNetwork is used to remove a content hash from a private ipfs network
func (api *API) removePinFromLocalHostForHostedIPFSNetwork(c *gin.Context) {
	username := GetAuthenticatedUserFromContext(c)
	hash := c.Param("hash")
	if _, err := gocid.Decode(hash); err != nil {
		FailOnError(c, err)
		return
	}
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}
	if err := CheckAccessForPrivateNetwork(username, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}
	rm := queue.IPFSPinRemoval{
		ContentHash: hash,
		NetworkName: networkName,
		UserName:    username,
	}
	mqConnectionURL := api.TConfig.RabbitMQ.URL
	qm, err := queue.Initialize(queue.IpfsPinRemovalQueue, mqConnectionURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnError(c, err)
		return
	}
	if err = qm.PublishMessageWithExchange(rm, queue.PinRemovalExchange); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    username,
	}).Info("private ipfs pin removal request sent to backend")

	Respond(c, http.StatusOK, gin.H{"response": "pin removal sent to backend"})
}

// GetLocalPinsForHostedIPFSNetwork is used to get local pins from the serving private ipfs node
func (api *API) getLocalPinsForHostedIPFSNetwork(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)
	if ethAddress != AdminAddress {
		FailNotAuthorized(c, "unauthorized access to admin route")
		return
	}
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}
	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}
	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	// initialize a connection toe the local ipfs node
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	// get all the known local pins
	// WARNING: THIS COULD BE A VERY LARGE LIST
	pinInfo, err := manager.Shell.Pins()
	if err != nil {
		api.LogError(err, IPFSPinParseError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs pin list requested")

	Respond(c, http.StatusOK, gin.H{"response": pinInfo})
}

// GetObjectStatForIpfsForHostedIPFSNetwork is  used to get object stats from a private ipfs network
func (api *API) getObjectStatForIpfsForHostedIPFSNetwork(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}
	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	key := c.Param("key")
	if _, err := gocid.Decode(key); err != nil {
		FailOnError(c, err)
		return
	}
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	stats, err := manager.ObjectStat(key)
	if err != nil {
		api.LogError(err, IPFSObjectStatError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs object stat requested")

	Respond(c, http.StatusOK, gin.H{"response": stats})
}

// CheckLocalNodeForPinForHostedIPFSNetwork is used to check the serving node for a pin
func (api *API) checkLocalNodeForPinForHostedIPFSNetwork(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)
	if ethAddress != AdminAddress {
		FailNotAuthorized(c, "unauthorized access to admin route")
		return
	}
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}
	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	hash := c.Param("hash")
	if _, err := gocid.Decode(hash); err != nil {
		FailOnError(c, err)
		return
	}
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	present, err := manager.ParseLocalPinsForHash(hash)
	if err != nil {
		api.LogError(err, IPFSPinParseError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs pin check requested")

	Respond(c, http.StatusOK, gin.H{"response": present})
}

// PublishDetailedIPNSToHostedIPFSNetwork is used to publish an IPNS record to a private network with fine grained control
func (api *API) publishDetailedIPNSToHostedIPFSNetwork(c *gin.Context) {

	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	ethAddress := GetAuthenticatedUserFromContext(c)

	mqURL := api.TConfig.RabbitMQ.URL

	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	um := models.NewUserManager(api.DBM.DB)
	qm, err := queue.Initialize(queue.IpnsEntryQueue, mqURL, true, false)
	if err != nil {
		api.LogError(err, QueueInitializationError)
		FailOnError(c, err)
		return
	}
	hash, present := c.GetPostForm("hash")
	if !present {
		FailNoExistPostForm(c, "hash")
		return
	}
	if _, err := gocid.Decode(hash); err != nil {
		FailOnError(c, err)
		return
	}
	lifetimeStr, present := c.GetPostForm("life_time")
	if !present {
		FailNoExistPostForm(c, "lifetime")
		return
	}
	ttlStr, present := c.GetPostForm("ttl")
	if !present {
		FailNoExistPostForm(c, "ttl")
		return
	}
	key, present := c.GetPostForm("key")
	if !present {
		FailNoExistPostForm(c, "key")
		return
	}
	resolveString, present := c.GetPostForm("resolve")
	if !present {
		FailNoExistPostForm(c, "resolve")
		return
	}

	ownsKey, err := um.CheckIfKeyOwnedByUser(ethAddress, key)
	if err != nil {
		api.LogError(err, KeySearchError)
		FailOnError(c, err)
		return
	}

	if !ownsKey {
		err = fmt.Errorf("unauthorized access to key by user %s", ethAddress)
		api.LogError(err, KeyUseError)
		FailOnError(c, err)
		return
	}

	resolve, err := strconv.ParseBool(resolveString)
	if err != nil {
		// user error, dont log
		FailOnError(c, err)
		return
	}
	lifetime, err := time.ParseDuration(lifetimeStr)
	if err != nil {
		// user error, dont log
		FailOnError(c, err)
		return
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		// user error, dont log
		FailOnError(c, err)
		return
	}
	ipnsUpdate := queue.IPNSEntry{
		CID:         hash,
		LifeTime:    lifetime,
		TTL:         ttl,
		Key:         key,
		Resolve:     resolve,
		NetworkName: networkName,
		UserName:    ethAddress,
	}
	if err := qm.PublishMessage(ipnsUpdate); err != nil {
		api.LogError(err, QueuePublishError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipns entry creation request sent to backend")

	Respond(c, http.StatusOK, gin.H{"response": "ipns entry creation request sent to backend"})
}

// CreateHostedIPFSNetworkEntryInDatabase is used to create an entry in the database for a private ipfs network
// TODO: make bootstrap peers and related config optional
func (api *API) createHostedIPFSNetworkEntryInDatabase(c *gin.Context) {
	// lock down as admin route for now
	ethAddress := GetAuthenticatedUserFromContext(c)
	if ethAddress != AdminAddress {
		FailNotAuthorized(c, "unauthorized access")
		return
	}

	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	apiURL, exists := c.GetPostForm("api_url")
	if !exists {
		FailNoExistPostForm(c, "api_url")
		return
	}

	swarmKey, exists := c.GetPostForm("swarm_key")
	if !exists {
		FailNoExistPostForm(c, "swarm_key")
		return
	}

	bPeers, exists := c.GetPostFormArray("bootstrap_peers")
	if !exists {
		FailNoExistPostForm(c, "bootstrap_peers")
		return
	}
	nodeAddresses, exists := c.GetPostFormArray("local_node_addresses")
	if !exists {
		FailNoExistPostForm(c, "local_node_addresses")
		return
	}
	users := c.PostFormArray("users")
	var localNodeAddresses []string
	var bootstrapPeerAddresses []string

	if len(nodeAddresses) != len(bPeers) {
		FailOnError(c, errors.New("length of local_node_addresses and bootstrap_peers must be equal"))
		return
	}
	for k, v := range bPeers {
		addr, err := utils.GenerateMultiAddrFromString(v)
		if err != nil {
			// this is entirely on the user, so lets not bother logging as it will just make noise
			FailOnError(c, err)
			return
		}
		valid, err := utils.ParseMultiAddrForIPFSPeer(addr)
		if err != nil {
			// this is entirely on the user, so lets not bother logging as it will just make noise
			FailOnError(c, err)
			return
		}
		if !valid {
			api.Logger.Errorf("provided peer %s is not a valid bootstrap peer", addr)
			FailOnError(c, fmt.Errorf("provided peer %s is not a valid bootstrap peer", addr))
			return
		}
		addr, err = utils.GenerateMultiAddrFromString(nodeAddresses[k])
		if err != nil {
			// this is entirely on the user, so lets not bother logging as it will just make noise
			FailOnError(c, err)
			return
		}
		valid, err = utils.ParseMultiAddrForIPFSPeer(addr)
		if err != nil {
			// this is entirely on the user, so lets not bother logging as it will just make noise
			FailOnError(c, err)
			return
		}
		if !valid {
			// this is entirely on the user, so lets not bother logging as it will just make noise
			FailOnError(c, fmt.Errorf("provided peer %s is not a valid ipfs peer", addr))
			return
		}
		bootstrapPeerAddresses = append(bootstrapPeerAddresses, v)
		localNodeAddresses = append(localNodeAddresses, nodeAddresses[k])
	}
	// previously we were initializing like `var args map[string]*[]string` which was causing some issues.
	args := make(map[string][]string)
	args["local_node_peer_addresses"] = localNodeAddresses
	if len(bootstrapPeerAddresses) > 0 {
		args["bootstrap_peer_addresses"] = bootstrapPeerAddresses
	}
	manager := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	network, err := manager.CreateHostedPrivateNetwork(networkName, apiURL, swarmKey, args, users)
	if err != nil {
		api.LogError(err, NetworkCreationError)
		FailOnError(c, err)
		return
	}
	um := models.NewUserManager(api.DBM.DB)

	if len(users) > 0 {
		for _, v := range users {
			if err := um.AddIPFSNetworkForUser(v, networkName); err != nil {
				api.LogError(err, NetworkCreationError)
				FailOnError(c, err)
				return
			}
		}
	} else {
		if err := um.AddIPFSNetworkForUser(AdminAddress, networkName); err != nil {
			api.LogError(err, NetworkCreationError)
			FailOnError(c, err)
			return
		}
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs network created")

	Respond(c, http.StatusOK, gin.H{"response": network})

}

// GetIPFSPrivateNetworkByName is used to get connection information for a priavate ipfs network
func (api *API) getIPFSPrivateNetworkByName(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)
	if ethAddress != AdminAddress {
		FailNotAuthorized(c, "unauthorized access")
		return
	}

	netName := c.Param("name")
	manager := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	net, err := manager.GetNetworkByName(netName)
	if err != nil {
		api.LogError(err, NetworkSearchError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs network by name requested")

	Respond(c, http.StatusOK, gin.H{"response": net})
}

// GetAuthorizedPrivateNetworks is used to get the private
// networks a user is authorized for
func (api *API) getAuthorizedPrivateNetworks(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)

	um := models.NewUserManager(api.DBM.DB)
	networks, err := um.GetPrivateIPFSNetworksForUser(ethAddress)
	if err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("authorized private ipfs network listing requested")

	Respond(c, http.StatusOK, gin.H{"response": networks})
}

// GetUploadsByNetworkName is used to getu plaods for a network by its name
func (api *API) getUploadsByNetworkName(c *gin.Context) {
	ethAddress := GetAuthenticatedUserFromContext(c)

	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	um := models.NewUploadManager(api.DBM.DB)
	uploads, err := um.FindUploadsByNetwork(networkName)
	if err != nil {
		api.LogError(err, UploadSearchError)
		FailOnError(c, err)
		return
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("uploads forprivate ifps network requested")

	Respond(c, http.StatusOK, gin.H{"response": uploads})
}

// DownloadContentHashForPrivateNetwork is used to download content from  a private ipfs network
func (api *API) downloadContentHashForPrivateNetwork(c *gin.Context) {
	networkName, exists := c.GetPostForm("network_name")
	if !exists {
		FailNoExistPostForm(c, "network_name")
		return
	}

	ethAddress := GetAuthenticatedUserFromContext(c)

	if err := CheckAccessForPrivateNetwork(ethAddress, networkName, api.DBM.DB); err != nil {
		api.LogError(err, PrivateNetworkAccessError)
		FailOnError(c, err)
		return
	}

	var contentType string
	// fetch the specified content type from the user
	contentType, exists = c.GetPostForm("content_type")
	// if not specified, provide a default
	if !exists {
		contentType = "application/octet-stream"
	}

	// get any extra headers the user might want
	exHeaders := c.PostFormArray("extra_headers")

	im := models.NewHostedIPFSNetworkManager(api.DBM.DB)
	apiURL, err := im.GetAPIURLByName(networkName)
	if err != nil {
		api.LogError(err, APIURLCheckError)
		FailOnError(c, err)
		return
	}
	// get the content hash that is to be downloaded
	contentHash := c.Param("hash")
	if _, err := gocid.Decode(contentHash); err != nil {
		FailOnError(c, err)
		return
	}
	// initialize our connection to IPFS
	manager, err := rtfs.Initialize("", apiURL)
	if err != nil {
		api.LogError(err, IPFSConnectionError)
		FailOnError(c, err)
		return
	}
	// read the contents of the file
	reader, err := manager.Shell.Cat(contentHash)
	if err != nil {
		api.LogError(err, IPFSCatError)
		FailOnError(c, err)
		return
	}
	// get the size of hte file in bytes
	sizeInBytes, err := manager.GetObjectFileSizeInBytes(contentHash)
	if err != nil {
		api.LogError(err, IPFSObjectStatError)
		FailOnError(c, err)
		return
	}
	// parse extra headers if there are any
	extraHeaders := make(map[string]string)
	var header string
	var value string
	// only process if there is actual data to process
	// this will always be admin locked
	if len(exHeaders) > 0 {
		// the array must be of equal length, as a header has two parts
		// the name of the header, and its value
		// this expects the user to have properly formatted the headers
		// we will need to restrict the headers that we process so we don't
		// open ourselves up to being attacked
		if len(exHeaders)%2 != 0 {
			FailOnError(c, errors.New("extra_headers post form is not even in length"))
			return
		}
		// parse through the available headers
		for i := 1; i < len(exHeaders)-1; i += 2 {
			// retrieve header name
			header = exHeaders[i-1]
			// retrieve header value
			value = exHeaders[i]
			// store data
			extraHeaders[header] = value
		}
	}

	api.Logger.WithFields(log.Fields{
		"service": "api",
		"user":    ethAddress,
	}).Info("private ipfs content download served")

	// send them the file
	c.DataFromReader(200, int64(sizeInBytes), contentType, reader, extraHeaders)
}
