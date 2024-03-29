package pioj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-redis/redis/v8"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestFileServer(t *testing.T) {
	os.Mkdir("../dist", os.FileMode(0755))
	if file, err := os.OpenFile("../dist/index.html", os.O_RDWR|os.O_CREATE, os.FileMode(0644)); err != nil {
		t.Error(err.Error())
	} else if fi, _ := os.Stat("../dist/index.html"); fi.Size() == 0 {
		file.WriteString("\n")
	}

	mux := http.NewServeMux()
	App{ServeMux: mux, Root: "../dist/", Fallback: "../dist/index.html"}.Handle()

	t.Run("GET/", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code at %s: %d", req.URL.Path, resp.StatusCode)
		}
	})

	t.Run("GET/404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/404", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code at %s: %d", req.URL.Path, resp.StatusCode)
		}
		contentType := resp.Header.Get("Content-Type")
		if contentType != "text/html; charset=utf-8" {
			t.Errorf("Unexpected Content-Type at %s: %s", req.URL.Path, contentType)
		}
	})
}

func connectDatabase() *mongo.Database {
	client, err := mongo.Connect(context.Background(), options.Client())
	if err != nil {
		panic(err)
	}
	database := client.Database("test")
	return database
}

func request(mux *http.ServeMux, method string, path string, body io.Reader) *http.Response {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, path, body))
	return w.Result()
}

func postjson(mux *http.ServeMux, path string, data any) *http.Response {
	body, _ := json.Marshal(data)
	return request(mux, http.MethodPost, path, bytes.NewBuffer(body))
}

func TestProblem(t *testing.T) {
	database := connectDatabase()
	database.Collection("problems").Drop(context.Background())
	mux := NewApp(Configuration{}, database, nil).ServeMux

	request_problem := func(path string, data any, i int) (Problem, error) {
		resp := postjson(mux, path, data)
		if resp.StatusCode != http.StatusOK {
			return Problem{}, fmt.Errorf("[%d] Unexpected status code at %s: %d", i, path, resp.StatusCode)
		}
		var problem Problem
		if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
			return problem, errors.New(err.Error())
		}
		if problem.ID != i {
			return problem, fmt.Errorf("[%d] Unexpected problem ID: %d", i, problem.ID)
		}
		return problem, nil
	}

	n := 3

	t.Run("create", func(t *testing.T) {
		for i := 1; i <= n; i++ {
			if _, err := request_problem(
				"/api/problem/create",
				map[string]map[string]string{
					"title":   {"en": fmt.Sprint("Problem ", i)},
					"content": {"en": "Content"},
				},
				i,
			); err != nil {
				t.Error(err.Error())
			}
		}
	})

	t.Run("create 400", func(t *testing.T) {
		resp := request(mux, http.MethodPost, "/api/problem/create", bytes.NewBufferString("{"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})

	t.Run("get", func(t *testing.T) {
		for i := 1; i <= n; i++ {
			if problem, err := request_problem("/api/problem/get", map[string]int{"id": i}, i); err != nil {
				t.Error(err.Error())
			} else {
				if problem.Title["en"] != fmt.Sprint("Problem ", i) {
					t.Errorf("[%d] Unexpected problem title: %s", i, problem.Title)
				}
			}
		}
	})

	t.Run("get 404", func(t *testing.T) {
		resp := postjson(mux, "/api/problem/get", map[string]int{"id": n + 1})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})

	t.Run("get 400", func(t *testing.T) {
		resp := request(mux, http.MethodPost, "/api/problem/get", bytes.NewBufferString("{"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})

	t.Run("get 500", func(t *testing.T) {
		database.Collection("problems").InsertOne(
			context.Background(),
			map[string]interface{}{"id": n + 1, "title": fmt.Sprint("Problem ", n+1)},
		)
		resp := postjson(mux, "/api/problem/get", map[string]int{"id": n + 1})
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})

	database.Client().Disconnect(context.Background())

	t.Run("create 500", func(t *testing.T) {
		resp := postjson(mux, "/api/problem/create", map[string]int{"id": n + 1})
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})
}

func TestUsers(t *testing.T) {
	database := connectDatabase()
	collection := database.Collection("users")
	collection.Drop(context.Background())
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{DB: 1})
	rdb.FlushDB(context.Background())
	mux := NewApp(Configuration{}, database, rdb).ServeMux

	t.Run("SetPassword and Save", func(t *testing.T) {
		user := User{Username: "username"}
		if _, err := user.Save(ctx, collection); err != nil {
			t.Error(err.Error())
		}
		if err := user.SetPassword("password"); err != nil {
			t.Fatal(err.Error())
		}
		if _, err := user.Save(ctx, collection); err != nil {
			t.Error(err.Error())
		}
		if !user.CheckPassword("password") {
			t.Error("Password should be valid")
		}
		if user.CheckPassword("pwd") {
			t.Error("Password should be invalid")
		}
	})

	t.Run("Unique", func(t *testing.T) {
		user := User{Username: "username"}
		if res, err := user.Create(ctx, collection); !mongo.IsDuplicateKeyError(err) {
			t.Error(res)
		}
		user.SetPassword("pwd")
		if _, err := user.Update(ctx, collection); err != nil {
			t.Error(err.Error())
		}
		user = User{Username: "another_username"}
		if _, err := user.Create(ctx, collection); err != nil {
			t.Error(err.Error())
		}
	})

	t.Run("Create", func(t *testing.T) {
		request_verification := func(email string) (code string) {
			resp := request(mux, http.MethodGet, "/api/user/email?email="+email, nil)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Unexpected status code: %d", resp.StatusCode)
			}
			code, err := rdb.Get(context.Background(), "pioj:verification:").Result()
			if err != nil {
				t.Error(err.Error())
			}
			return
		}
		request_create := func(user UserWithPasswordAndVerification, status int) (resp *http.Response) {
			resp = postjson(mux, "/api/user/create", user)
			if resp.StatusCode != status {
				t.Errorf("Unexpected status code: %d", resp.StatusCode)
			}
			return
		}
		user := UserWithPasswordAndVerification{User: User{Username: ""}}
		resp := request(mux, http.MethodGet, "/api/user/create", bytes.NewBufferString("{"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		request_create(user, http.StatusUnauthorized)
		request_verification("")
		request_create(user, http.StatusUnauthorized)
		user.Verification = request_verification("")
		request_create(user, http.StatusOK)
		resp = request(mux, http.MethodGet, "/api/user/email?email=@", nil)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		user.Verification = request_verification("")
		database.Client().Disconnect(context.Background())
		request_create(user, http.StatusInsufficientStorage)
		rdb.Close()
		resp = request(mux, http.MethodGet, "/api/user/email", nil)
		if resp.StatusCode != http.StatusInsufficientStorage {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		request_create(user, http.StatusInsufficientStorage)
	})

	database = connectDatabase()
	mux = NewApp(Configuration{}, database, rdb).ServeMux
	t.Run("Login", func(t *testing.T) {
		resp := request(mux, http.MethodPost, "/api/user/login", bytes.NewBufferString(""))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		resp = postjson(mux, "/api/user/login", UsernameAndPassword{Username: "username", Password: "password"})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		resp = postjson(mux, "/api/user/login", UsernameAndPassword{Username: "username", Password: "pwd"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
		database.Client().Disconnect(context.Background())
		resp = postjson(mux, "/api/user/login", UsernameAndPassword{Username: "username", Password: "password"})
		if resp.StatusCode != http.StatusInsufficientStorage {
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}
	})
}
