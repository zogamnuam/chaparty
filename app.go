package app

import (
	"bytes"
	"image"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	fb "github.com/huandu/facebook"

	"appengine"
	"appengine/datastore"
	"appengine/urlfetch"
)

var clientID = "526791527487217"
var FbApp = fb.New(clientID, APPSECRET)

var aboutParams = fb.Params{
	"method":       fb.GET,
	"relative_url": "me",
	"fields":       "name,email,gender,age_range,hometown",
}

var photoParams = fb.Params{
	"method":       fb.GET,
	"relative_url": "me/picture?width=320&height=320&redirect=false",
}

func init() {
	http.HandleFunc("/static/", StaticHandler)
	http.HandleFunc("/", MainHandler)
	http.HandleFunc("/api/", APIHandler)

	fb.Debug = fb.DEBUG_ALL
	FbApp.EnableAppsecretProof = true
}

func APIHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	code := query.Get("code")

	if code == "" {
		http.Error(w, "Cannot get code from facebook.", 505)
		return
	}

	context := appengine.NewContext(r)
	client := urlfetch.Client(context)
	fb.SetHttpClient(client)

	redirectURL := "http://" + r.Host + r.URL.Path
	accessResp, err := fb.Get("/v2.4/oauth/access_token", fb.Params{
		"code":          code,
		"redirect_uri":  redirectURL,
		"client_id":     clientID,
		"client_secret": APPSECRET,
	})
	check(err, context)

	var accessToken string
	accessResp.DecodeField("access_token", &accessToken)

	paths := strings.Split(r.URL.Path, "/")
	party := paths[len(paths)-1]

	session := FbApp.Session(accessToken)
	session.HttpClient = urlfetch.Client(context)
	err = session.Validate()
	check(err, context)

	results, err := session.BatchApi(aboutParams, photoParams)
	check(err, context)

	aboutBatch, err := results[0].Batch()
	check(err, context)
	photoBatch, err := results[1].Batch()
	check(err, context)

	aboutResp := aboutBatch.Result
	photoResp := photoBatch.Result

	SaveAboutUser(&aboutResp, context)
	profilePicture := GetUserPhoto(&photoResp, context)

	imagebytes := addLogo(profilePicture, party, context)
	form, mime := CreateImageForm(&imagebytes, context, r.Host)

	url := "https://graph.facebook.com/v2.4/me/photos" +
		"?access_token=" + accessToken +
		"&appsecret_proof=" + session.AppsecretProof()

	uploadResquest, _ := http.NewRequest("POST", url, form)
	uploadResquest.Header.Set("Content-Type", mime)
	uploadResp, _ := session.Request(uploadResquest)
	check(err, context)

	var photoID string
	uploadResp.DecodeField("id", &photoID)
	redirectUrl := "https://facebook.com/photo.php?fbid=" + photoID + "&makeprofile=1&prof"
	http.Redirect(w, r, redirectUrl, 303)
}

func StaticHandler(w http.ResponseWriter, r *http.Request) {
	path := "." + r.URL.Path

	if f, err := os.Stat(path); err == nil && !f.IsDir() {
		http.ServeFile(w, r, path)
		return
	}

	http.NotFound(w, r)
}

func MainHandler(w http.ResponseWriter, r *http.Request) {
	switch {

	case r.URL.Path == "/privacy":
		w.Write([]byte("Coming Soon"))

	case r.URL.Path != "/":
		http.NotFound(w, r)

	default:
		handleMain(w, r)
	}

	return
}

func handleMain(w http.ResponseWriter, r *http.Request) {
	context := appengine.NewContext(r)
	data, err := ioutil.ReadFile("index.html")
	check(err, context)
	w.Write(data)
}

type Log struct {
	Name     string
	Gender   string
	Party    string
	Email    string
	AgeRange string
	Hometown string
}

func SaveAboutUser(aboutResp *fb.Result, context appengine.Context) {
	var log Log
	aboutResp.Decode(&log)

	var ageRange map[string]string
	aboutResp.DecodeField("age_range", &ageRange)
	log.AgeRange = ageRange["min"]

	_, err := datastore.Put(context,
		datastore.NewIncompleteKey(context, "log", nil),
		&log)
	check(err, context)
}

func GetUserPhoto(photoResp *fb.Result, context appengine.Context) *image.Image {
	var dataField fb.Result
	photoResp.DecodeField("data", &dataField)

	var url string
	dataField.DecodeField("url", &url)

	client := urlfetch.Client(context)
	resp, err := client.Get(url)
	data, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	check(err, context)

	reader := bytes.NewReader(data)
	profilePicture, _, err := image.Decode(reader)
	check(err, context)

	return &profilePicture
}

func CreateImageForm(imageBytes *[]byte, context appengine.Context, host string) (*bytes.Buffer, string) {
	var formBuffer bytes.Buffer
	multiWriter := multipart.NewWriter(&formBuffer)

	imageField, err := multiWriter.CreateFormFile("source", "election.png")
	check(err, context)

	imageBuffer := bytes.NewBuffer(*imageBytes)
	_, err = io.Copy(imageField, imageBuffer)
	check(err, context)

	messageField, err := multiWriter.CreateFormField("caption")
	check(err, context)
	_, err = messageField.Write([]byte("Created at http://" + host))
	check(err, context)

	multiWriter.Close()
	return &formBuffer, multiWriter.FormDataContentType()
}
