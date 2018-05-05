package api

import (
	"fmt"
	"net/http"

	"github.com/RTradeLtd/RTC-IPFS/rtfs"
	"github.com/gin-gonic/gin"
)

// Setup is used to build our routes
func Setup() *gin.Engine {
	r := gin.Default()
	setupRoutes(r)
	return r
}

func setupRoutes(g *gin.Engine) {

	g.POST("/api/v1/ipfs/pin-hash/:hash", pinHash)
	g.POST("/api/v1/ipfs/add-file", addFile)
}

func pinHash(c *gin.Context) {
	hash := c.Param("hash")
	manager := rtfs.Initialize()
	err := manager.Shell.Pin(hash)
	if err != nil {
		c.JSON(404, nil)
	}
	c.JSON(http.StatusOK, gin.H{"hash": hash})
}

func addFile(c *gin.Context) {
	file, _ := c.FormFile("file")

	fmt.Println(file)
}