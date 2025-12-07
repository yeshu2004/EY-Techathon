package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
	"google.golang.org/genai"
)

var (
	PORT     = ":4000"
	minScore = int64(700)
	maxScore = int64(900)
	suffPath = "./uploads/"
)

type ErrorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type Customer struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Age              int    `json:"age"`
	City             string `json:"city"`
	CurrentLoan      string `json:"currentLoan"`
	CreditScore      int    `json:"creditScore"`
	PreApprovedLimit int    `json:"preApprovedLimit"`
}

var Customers = []Customer{
	{1, "Amit Sharma", 32, "Mumbai", "Home Loan – 1800000 @ 9.2%", 782, 250000},
	{2, "Sneha Gupta", 28, "Bengaluru", "None", 815, 300000},
	{3, "Rohan Verma", 41, "Delhi", "Car Loan – 650000 @ 10.5%", 695, 175000},
	{4, "Priya Nair", 35, "Kochi", "Credit Card EMI – 45000", 730, 220000},
	{5, "Kunal Mehta", 29, "Pune", "Bike Loan – 85000 @ 11.2%", 760, 150000},
	{6, "Ankita Singh", 27, "Jaipur", "None", 840, 350000},
	{7, "Rahul Chauhan", 45, "Lucknow", "Home Loan – 2200000 @ 8.9%", 702, 200000},
	{8, "Shivani Deshpande", 33, "Nagpur", "Personal Loan – 120000 @ 13%", 665, 100000},
	{9, "Deepak Soni", 38, "Indore", "Car Loan – 520000 @ 10%", 720, 180000},
	{10, "Megha Trivedi", 30, "Ahmedabad", "None", 799, 275000},
}

type Handler struct {
	db *sql.DB
}

func main() {

	db, err := connectDB()
	if err != nil {
		fmt.Printf("error in db: %v\n", err)
		return
	}
	defer db.Close()
	h := &Handler{
		db: db,
	}
	http.HandleFunc("/credit-score", h.fetchCreditScore)
	http.HandleFunc("/upload-salary", uploadSalaryDoc)

	fmt.Printf("server running on port: %v\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("server start error: %v", err)
		return
	}
}

func connectDB() (*sql.DB, error) {
	if err := godotenv.Load(".env"); err != nil {
		return nil, fmt.Errorf("error loading .env file: %v", err)
	}

	cfg := mysql.NewConfig()
	cfg.User = os.Getenv("DBUSER")
	cfg.Passwd = os.Getenv("DBPASS")
	cfg.Net = "tcp"
	cfg.Addr = "127.0.0.1:3306"
	cfg.DBName = "loandb"
	cfg.ParseTime = true

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("error opening database: %v", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("error pinging database: %v", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	fmt.Println("Connected to SQL Database!")
	return db, nil
}

func (h *Handler) fetchCreditScore(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// fetch the id from middleware
	userId := 1

	// parse & fetch loadAmout
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		log.Printf("parse error: %v\n", err)
		return
	}
	loanAmount := r.FormValue("loan_amount")
	loan, err := strconv.Atoi(loanAmount)
	if err != nil {
		log.Println(err)
		return
	}

	// get the credit score
	var (
		creditScore  int64
		salary       int64
		existingEMIs int64
	)
	if err := h.db.QueryRow("SELECT credit_score, monthly_income, existing_emi FROM USER WHERE id = ?", userId).Scan(&creditScore, &salary, &existingEMIs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Printf("db credit score scan error: %v\n", err)
		return
	}

	// check for threshold score
	if creditScore < minScore {
		http.Error(w, "low credit score", http.StatusBadRequest)
		log.Printf("cannot procede further, low credit score.")
		return
	}

	// calculate pre-approved limit
	limitAmount := preApprovedLimit(creditScore, salary, existingEMIs)

	switch {
	// instant approval i.e loan < L
	case int(loan) < int(limitAmount):

	// document req  i.e L < loan ≤ 2L
	case int(limitAmount) < int(loan) && int(loan) < int(2*limitAmount):

	// reject i.e loan > 2L
	case int(loan) < int(2*limitAmount):

	default:
		fmt.Println("idk what to print")
	}

}

func preApprovedLimit(c int64, s int64, e int64) float64 {
	csf := float64(c / maxScore)
	er := float64(s-e) / float64(s)
	return float64(s*10) * csf * er
}

// TODO: mine type, only pdf required
func uploadSalaryDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form data, limit to 10MB
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the file from the form data
	file, handler, err := r.FormFile("pdfFile")
	if err != nil {
		http.Error(w, "Error retrieving file from form", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := os.MkdirAll(suffPath, os.ModePerm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fullPath := suffPath + strings.Join(strings.Split(handler.Filename, " "), "")
	dst, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file content to the new file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "Successfully uploaded file: %s: %v", handler.Filename)
}

// TODO: 
func ocrFile(w http.ResponseWriter) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	
}
