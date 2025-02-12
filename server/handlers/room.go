package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/maruel/natural"
	"github.com/synctv-org/synctv/internal/db"
	dbModel "github.com/synctv-org/synctv/internal/model"
	"github.com/synctv-org/synctv/internal/op"
	"github.com/synctv-org/synctv/server/middlewares"
	"github.com/synctv-org/synctv/server/model"
	"github.com/zijiren233/gencontainer/vec"
)

var (
	ErrAuthFailed  = errors.New("auth failed")
	ErrAuthExpired = errors.New("auth expired")
	ErrRoomAlready = errors.New("room already exists")
)

type FormatErrNotSupportPosition string

func (e FormatErrNotSupportPosition) Error() string {
	return fmt.Sprintf("not support position %s", string(e))
}

func CreateRoom(ctx *gin.Context) {
	user := ctx.MustGet("user").(*op.User)
	req := model.CreateRoomReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	r, err := user.CreateRoom(req.RoomName, req.Password, db.WithSetting(req.Setting))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	room, err := op.LoadRoom(r)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewApiErrorResp(err))
		return
	}

	token, err := middlewares.NewAuthRoomToken(user, room)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewApiErrorResp(err))
		return
	}

	ctx.JSON(http.StatusCreated, model.NewApiDataResp(gin.H{
		"roomId": room.ID,
		"token":  token,
	}))
}

func RoomList(ctx *gin.Context) {
	r := op.GetAllRoomsWithoutHidden()
	resp := vec.New[*model.RoomListResp](vec.WithCmpLess[*model.RoomListResp](func(v1, v2 *model.RoomListResp) bool {
		return v1.PeopleNum < v2.PeopleNum
	}), vec.WithCmpEqual[*model.RoomListResp](func(v1, v2 *model.RoomListResp) bool {
		return v1.PeopleNum == v2.PeopleNum
	}))
	for _, v := range r {
		resp.Push(&model.RoomListResp{
			RoomId:       v.ID,
			RoomName:     v.Name,
			PeopleNum:    v.ClientNum(),
			NeedPassword: v.NeedPassword(),
			Creator:      op.GetUserName(v.Room.CreatorID),
			CreatedAt:    v.Room.CreatedAt.UnixMilli(),
		})
	}

	switch ctx.DefaultQuery("sort", "peopleNum") {
	case "peopleNum":
		resp.SortStable()
	case "creator":
		resp.SortStableFunc(func(v1, v2 *model.RoomListResp) bool {
			return natural.Less(v1.Creator, v2.Creator)
		}, func(t1, t2 *model.RoomListResp) bool {
			return t1.Creator == t2.Creator
		})
	case "createdAt":
		resp.SortStableFunc(func(v1, v2 *model.RoomListResp) bool {
			return v1.CreatedAt < v2.CreatedAt
		}, func(t1, t2 *model.RoomListResp) bool {
			return t1.CreatedAt == t2.CreatedAt
		})
	case "roomName":
		resp.SortStableFunc(func(v1, v2 *model.RoomListResp) bool {
			return natural.Less(v1.RoomName, v2.RoomName)
		}, func(t1, t2 *model.RoomListResp) bool {
			return t1.RoomName == t2.RoomName
		})
	case "roomId":
		resp.SortStableFunc(func(v1, v2 *model.RoomListResp) bool {
			return v1.RoomId < v2.RoomId
		}, func(t1, t2 *model.RoomListResp) bool {
			return t1.RoomId == t2.RoomId
		})
	case "needPassword":
		resp.SortStableFunc(func(v1, v2 *model.RoomListResp) bool {
			return v1.NeedPassword && !v2.NeedPassword
		}, func(t1, t2 *model.RoomListResp) bool {
			return t1.NeedPassword == t2.NeedPassword
		})
	default:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorStringResp("sort must be peoplenum or roomid"))
		return
	}

	switch ctx.DefaultQuery("order", "desc") {
	case "asc":
		// do nothing
	case "desc":
		resp.Reverse()
	default:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorStringResp("order must be asc or desc"))
		return
	}

	list, err := GetPageItems(ctx, resp.Slice())
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewApiDataResp(gin.H{
		"total": resp.Len(),
		"list":  list,
	}))
}

func CheckRoom(ctx *gin.Context) {
	id, err := strconv.Atoi(ctx.Query("roomId"))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	r, err := op.GetRoomByID(uint(id))
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotFound, model.NewApiErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewApiDataResp(gin.H{
		"peopleNum":    r.ClientNum(),
		"needPassword": r.NeedPassword(),
	}))
}

func LoginRoom(ctx *gin.Context) {
	user := ctx.MustGet("user").(*op.User)

	req := model.LoginRoomReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	room, err := middlewares.AuthRoomWithPassword(user, req.RoomId, req.Password)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusUnauthorized, model.NewApiErrorResp(err))
		return
	}

	token, err := middlewares.NewAuthRoomToken(user, room)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewApiErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewApiDataResp(gin.H{
		"roomId": room.ID,
		"token":  token,
	}))
}

func DeleteRoom(ctx *gin.Context) {
	room := ctx.MustGet("room").(*op.Room)
	user := ctx.MustGet("user").(*op.User)

	if !user.HasPermission(room, dbModel.CanDeleteRoom) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewApiErrorStringResp("you don't have permission to delete room"))
		return
	}

	err := op.DeleteRoom(room)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewApiErrorResp(err))
		return
	}

	ctx.Status(http.StatusNoContent)
}

func SetRoomPassword(ctx *gin.Context) {
	room := ctx.MustGet("room").(*op.Room)
	user := ctx.MustGet("user").(*op.User)

	if !user.HasPermission(room, dbModel.CanSetRoomPassword) {
		ctx.AbortWithStatusJSON(http.StatusForbidden, model.NewApiErrorStringResp("you don't have permission to set room password"))
		return
	}

	req := model.SetRoomPasswordReq{}
	if err := model.Decode(ctx, &req); err != nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, model.NewApiErrorResp(err))
		return
	}

	token, err := middlewares.NewAuthUserToken(user)
	if err != nil {
		ctx.AbortWithStatusJSON(http.StatusInternalServerError, model.NewApiErrorResp(err))
		return
	}

	ctx.JSON(http.StatusOK, model.NewApiDataResp(gin.H{
		"roomId": room.ID,
		"token":  token,
	}))
}

func RoomSetting(ctx *gin.Context) {
	room := ctx.MustGet("room").(*op.Room)
	// user := ctx.MustGet("user").(*op.User)

	ctx.JSON(http.StatusOK, model.NewApiDataResp(gin.H{
		"hidden":       room.Setting.Hidden,
		"needPassword": room.NeedPassword(),
	}))
}
