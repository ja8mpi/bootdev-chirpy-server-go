package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/ja8mpi/bootdev-chirpy-server-go/internal/database"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type parameters struct {
	Body string `body:"age"`
}

type errorReturnVals struct {
	Error string `json:"error"`
}

type valiedReturnVals struct {
	Valid bool `json:"valid"`
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
}

type cleanedReturnVals struct {
	CleanedBody string `json:"cleaned_body"`
}

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
	platform       string
	db             *sql.DB
}

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (cfg *apiConfig) getMetricsHandler(w http.ResponseWriter, r *http.Request) {
	count := cfg.fileserverHits.Load()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	htmlTemplate := `<html>
					<body>
						<h1>Welcome, Chirpy Admin</h1>
						<p>Chirpy has been visited %d times!</p>
					</body>
					</html>`
	fmt.Fprintf(w, htmlTemplate, count)
}

func (cfg *apiConfig) resetMetricsHandler(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func chirpHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)

	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		w.WriteHeader(500)
		return
	}

	if len(params.Body) > 140 {

		respBody := errorReturnVals{
			Error: "Chirp is too long",
		}
		dat, err := json.Marshal(respBody)
		if err != nil {
			log.Printf("Error marshalling JSON: %s", err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write(dat)
		return
	}

	//check if params contain forbidden words
	processedWords, _ := processWords(params.Body)

	cleanedBody := cleanedReturnVals{
		CleanedBody: processedWords,
	}
	dat, err := json.Marshal(cleanedBody)
	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(dat)
}

func hasPunctuation(s string) bool {
	for _, r := range s {
		if unicode.IsPunct(r) {
			return true
		}
	}
	return false
}

func processWords(input string) (string, error) {
	banned := []string{"kerfuffle", "sharbert", "fornax"}
	lowerWords := strings.Split(strings.ToLower(input), " ")
	originalWords := strings.Split(input, " ")

	foundBadWord := false

	for i, word := range lowerWords {
		for _, banned := range banned {
			if banned == word {
				if !hasPunctuation(word) {
					originalWords[i] = "****"
					foundBadWord = true
				}
			}
		}
	}

	input = strings.Join(originalWords, " ")
	if foundBadWord {
		return input, errors.New("Bad word found in text")
	}
	return input, nil
}

func createUser(db *sql.DB) (db *sql.DB, username, password string) error {
    query := `INSERT INTO users (username, password) VALUES ($1, $2)`
    _, err := db.Exec(query, username, password)
    return err
}

func (cfg *apiConfig) createUserHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)

	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		w.WriteHeader(500)
		return
	}

	user, err := cfg.db.CreateUser(r.Context(), params.Email)

	fmt.Println(params.Body)
	dat, _ := json.Marshal(params.email)
	w.WriteHeader(http.StatusOK)
	w.Write(dat)
}

func respondWithError(w http.ResponseWriter, code int, msg string)         {}
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {}

func main() {
	godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	db, err := sql.Open("postgres", dbURL)

	if err != nil {
		return
	}

	dbQueries := database.New(db)

	mux := http.NewServeMux()

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux, // Use the new ServeMux
	}
	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		dbQueries:      dbQueries,
		platform:       platform,
		db:             db,
	}

	// File server at /app/
	fs := http.FileServer(http.Dir("."))
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", fs)))

	mux.HandleFunc("GET /api/healthz", readinessHandler)

	mux.HandleFunc("GET /admin/metrics", apiCfg.getMetricsHandler) // fixed method reference
	mux.HandleFunc("POST /admin/reset", apiCfg.resetMetricsHandler)
	mux.HandleFunc("POST /api/validate_chirp", chirpHandler)
	mux.HandleFunc("POST /api/users", apiCfg.createUserHandler)

	// Start the server
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
