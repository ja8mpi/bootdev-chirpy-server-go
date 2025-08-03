package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/ja8mpi/bootdev-chirpy-server-go/internal/database"
	"github.com/ja8mpi/go-auth"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type parameters struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type chripParams struct {
	Body   string    `json:"body"`
	UserId uuid.UUID `json:"user_id"`
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
	Token     string    `json:"token"`
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

type cleanedReturnVals struct {
	CleanedBody string `json:"cleaned_body"`
}

type apiConfig struct {
	fileserverHits atomic.Int32
	dbQueries      *database.Queries
	platform       string
	db             *sql.DB
	jwtSign        string
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
	if cfg.platform != "dev" {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
		return
	}

	err := cfg.dbQueries.DeleteAllUsers(r.Context())
	if err != nil {
		fmt.Printf("failed to delete all users: %v\n", err)
		http.Error(w, "failed to delete users", http.StatusInternalServerError)
		return
	}

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

func (cfg *apiConfig) chirpHandler(w http.ResponseWriter, r *http.Request) {

	// Decode JSON params
	decoder := json.NewDecoder(r.Body)
	params := chripParams{}
	if err := decoder.Decode(&params); err != nil {
		fmt.Printf("Error decoding parameters: %s\n", err)
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}
	fmt.Printf("Decoded params: %+v\n", params)
	// Get bearer token from headers
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		fmt.Printf("Error checking Bearer parameters: %s\n", err)
		http.Error(w, "Missing or invalid authorization header", http.StatusUnauthorized)
		return
	}

	// Validate JWT token
	userid, err := auth.ValidateJWT(token, cfg.jwtSign)
	if err != nil {
		fmt.Printf("Invalid token: %s\n", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if _, err := cfg.dbQueries.GetUserByID(r.Context(), userid); err != nil {
		// You may want to check for specific "not found" error if your DB driver supports it
		http.Error(w, "User not found", http.StatusBadRequest)
		return
	}

	// Check chirp length
	if len(params.Body) > 140 {
		respBody := errorReturnVals{Error: "Chirp is too long"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(respBody); err != nil {
			fmt.Printf("Error marshalling JSON: %s\n", err)
		}
		return
	}

	// Process forbidden words
	processedWords, _ := processWords(params.Body)
	cleanedBody := processedWords

	// Create chirp in DB
	dbChirp, err := cfg.dbQueries.CreateChirp(r.Context(), database.CreateChirpParams{
		Body:   cleanedBody,
		UserID: userid,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Return created chirp
	chirp := Chirp{
		ID:        dbChirp.ID,
		Body:      dbChirp.Body,
		UserID:    userid,
		CreatedAt: dbChirp.CreatedAt,
		UpdatedAt: dbChirp.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(chirp)
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

func (cfg *apiConfig) createUserHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var params parameters
	err := decoder.Decode(&params)

	if err != nil {
		fmt.Printf("Error decoding parameters: %s\n", err)
		w.WriteHeader(500)
		return
	}

	pwd, err := auth.HashPassword(params.Password)
	if err != nil {
		fmt.Printf("Error hashing password: %s\n", err)
		w.WriteHeader(500)
		return
	}

	user, err := cfg.dbQueries.CreateUser(r.Context(), database.CreateUserParams{
		Email:          params.Email,
		HashedPassword: pwd,
	})

	if err != nil {
		fmt.Printf("%v\n", err)
		w.WriteHeader(500)
	}

	dat := User{
		ID:        user.ID,
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dat)
}

func (cfg *apiConfig) getChirps(w http.ResponseWriter, r *http.Request) {
	dbChirps, err := cfg.dbQueries.GetChirps(r.Context())
	if err != nil {
		w.WriteHeader(500)
		return
	}

	var chirps []Chirp

	for _, ch := range dbChirps {
		chirps = append(chirps, Chirp{
			ID:        ch.ID,
			Body:      ch.Body,
			UserID:    ch.UserID,
			CreatedAt: ch.CreatedAt,
			UpdatedAt: ch.UpdatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chirps)
}

func (cfg *apiConfig) getChirpById(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("chirpID")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid UUID", http.StatusBadRequest)
		return
	}

	dbChirp, err := cfg.dbQueries.GetChirpByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Chirp not found", http.StatusNotFound)
		return
	}

	chirp := Chirp{
		ID:        dbChirp.ID,
		Body:      dbChirp.Body,
		UserID:    dbChirp.UserID,
		CreatedAt: dbChirp.CreatedAt,
		UpdatedAt: dbChirp.UpdatedAt,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(chirp)
}

func respondWithError(w http.ResponseWriter, code int, msg string)         {}
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {}

func (cfg *apiConfig) loginHandler(w http.ResponseWriter, r *http.Request) {

	decoder := json.NewDecoder(r.Body)
	var params parameters
	err := decoder.Decode(&params)

	if err != nil {
		fmt.Printf("Error decoding parameters: %s\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	dbUser, err := cfg.dbQueries.GetUserByEmail(r.Context(), params.Email)
	if err != nil {
		fmt.Printf("Error getting user: %s\n", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	err = auth.CheckPasswordHash(params.Password, dbUser.HashedPassword)
	if err != nil {
		fmt.Printf("Wrong password: %s\n", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	const maxExpirationSeconds = int(time.Hour / time.Second) // 3600

	if params.ExpiresInSeconds <= 0 || params.ExpiresInSeconds > maxExpirationSeconds {
		params.ExpiresInSeconds = maxExpirationSeconds
	}

	// Create the token
	token, err := auth.MakeJWT(dbUser.ID, cfg.jwtSign, time.Duration(params.ExpiresInSeconds)*time.Second)

	if err != nil {
		fmt.Printf("Error creating token %s\n", err)
		http.Error(w, `{"error":"Invalid email or password"}`, http.StatusUnauthorized)
		return
	}

	user := User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
		Token:     token,
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(user); err != nil {
		fmt.Printf("Failed to write response: %s\n", err)
	}
}

func main() {
	godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	jwtSign := os.Getenv("JWT_SIGN")
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
		jwtSign:        jwtSign,
	}

	// File server at /app/
	fs := http.FileServer(http.Dir("."))
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", fs)))

	mux.HandleFunc("GET /api/healthz", readinessHandler)

	mux.HandleFunc("GET /admin/metrics", apiCfg.getMetricsHandler) // fixed method reference
	mux.HandleFunc("POST /admin/reset", apiCfg.resetMetricsHandler)
	mux.HandleFunc("POST /api/users", apiCfg.createUserHandler)
	mux.HandleFunc("POST /api/chirps", apiCfg.chirpHandler)
	mux.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.getChirpById)
	mux.HandleFunc("GET /api/chirps", apiCfg.getChirps)
	mux.HandleFunc("POST /api/login", apiCfg.loginHandler)

	// Start the server
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
