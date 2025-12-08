package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
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

type LoanStage1Response struct {
	LoanAmount   int     `json:"loan_amount"`
	Duration     int     `json:"duration_months"`
	MonthlyEMI   float64 `json:"monthly_emi"`
	LimitAmount  int     `json:"limit_amount"`
	Status       string  `json:"status"`
	Message      string  `json:"message"`
}

type Stage2Response struct {
	LoanAmount int     `json:"loan_amount"`
	MonthlyEMI float64 `json:"monthly_emi"`
	Salary     int     `json:"salary"`
	Status     string  `json:"status"`
	Message    string  `json:"message"`
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

func (h *Handler) ApplyLoanHandler(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type", "application/json")

	// parse & fetch loadAmout
	if err := r.ParseForm(); err != nil {
		http.Error(w, `{"error":"failed to parse form"}`, http.StatusBadRequest)
		return
	}
	
	loanAmount := r.FormValue("loan_amount")
	months := r.FormValue("duration")
	loan, err := strconv.Atoi(loanAmount)
	if err != nil || loan <= 0 {
		http.Error(w, `{"error":"invalid loan amount"}`, http.StatusBadRequest)
		return
	}

	duration, err := strconv.Atoi(months)
	if err != nil || duration <= 0 {
		http.Error(w, `{"error":"invalid duration"}`, http.StatusBadRequest)
		return
	}

	monthlyEMI := float64(loan) / float64(duration)

	limitAmount, err :=  h.fetchLimitAmount();
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	resp := LoanStage1Response{
		LoanAmount:  loan,
		Duration:    duration,
		MonthlyEMI:  monthlyEMI,
		LimitAmount: limitAmount,
	}


	switch {
	// instant approval i.e loan < L
	case int(loan) < int(limitAmount):
		resp.Status = "approved"
		resp.Message = "instant approval granted"
	// document req  i.e L < loan ≤ 2L
	case int(limitAmount) < int(loan) && int(loan) < int(2*limitAmount):
		resp.Status = "salary slip needed"
		resp.Message = "please upload salary slip for further verification"
	// reject i.e loan > 2L
	case int(loan) > int(2*limitAmount):
		resp.Status = "rejected"
		resp.Message = "requested loan exceeds maximum eligibility"
	default:
		resp.Status = "unkown"
		resp.Message = "unable to determine eligibility."
	}

	json.NewEncoder(w).Encode(resp)
}

// TODO: mine type, only pdf required
func UploadSalarySlipHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	
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

	b, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ocrFile(w, b)
	fmt.Fprintf(w, "Successfully uploaded file: %s", handler.Filename)

}

// func (h *Handler) fetchCreditScore(w http.ResponseWriter, r *http.Request) {
// 	// w.Header().Set("Content-Type", "application/json")
// 	// fetch the id from middleware
// 	userId := 1
// 	// get the credit score
// 	var (
// 		creditScore  int64
// 		salary       int64
// 		existingEMIs int64
// 	)
// 	if err := h.db.QueryRow("SELECT credit_score, monthly_income, existing_emi FROM USER WHERE id = ?", userId).Scan(&creditScore, &salary, &existingEMIs); err != nil {
// 		http.Error(w, err.Error(), http.StatusBadRequest)
// 		log.Printf("db credit score scan error: %v\n", err)
// 		return
// 	}

// 	// check for threshold score
// 	if creditScore < minScore {
// 		http.Error(w, "low credit score", http.StatusBadRequest)
// 		log.Printf("cannot procede further, low credit score.")
// 		return
// 	}

// 	// calculate pre-approved limit
// 	limitAmount := preApprovedLimit(creditScore, salary, existingEMIs)

// 	// switch {
// 	// // instant approval i.e loan < L
// 	// case int(loan) < int(limitAmount):

// 	// // document req  i.e L < loan ≤ 2L
// 	// case int(limitAmount) < int(loan) && int(loan) < int(2*limitAmount):

// 	// // reject i.e loan > 2L
// 	// case int(loan) < int(2*limitAmount):

// 	// default:
// 	// 	fmt.Println("idk what to print")
// 	// }

// }


func (h *Handler) fetchLimitAmount() (int, error) {
	userId := 1

	var (
		creditScore  int64
		salary       int64
		existingEMIs int64
	)
	if err := h.db.QueryRow("SELECT credit_score, monthly_income, existing_emi FROM USER WHERE id = ?", userId).Scan(&creditScore, &salary, &existingEMIs); err != nil {
		log.Printf("db credit score scan error: %v\n", err)
		return -1, err
	}

	// check for threshold score
	if creditScore < minScore {
		log.Printf("cannot procede further, low credit score.")
		return -1, fmt.Errorf("low credit score") 
	}

	// calculate pre-approved limit
	limitAmount := preApprovedLimit(creditScore, salary, existingEMIs)
	return  int(limitAmount), nil
}

func ocrFile(w http.ResponseWriter, b []byte) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	parts := []*genai.Part{
		{
			InlineData: &genai.Blob{
				MIMEType: "application/pdf",
				Data:     b,
			},
		},
		genai.NewPartFromText(`
	You are an OCR + information extraction system.

	TASK:
	1. Read the PDF.
	2. Extract the employee's salary if present.
	3. Salary may appear under any of these labels:
	- Salary
	- Net Salary
	- Net Pay
	- Net Amount
	- Gross Salary
	- Monthly Salary
	- Earnings Total
	- Total Pay
	- CTC Monthly
	4. Extract ONLY the numeric value (₹, commas allowed).
	5. If there is no salary-like amount, or the document is NOT a payslip/ salary document:
		RETURN EXACTLY: WRONG_DOCS

	RULES:
	- NEVER guess.
	- If multiple salary values exist, choose the final take-home salary / net salary.
	- Return ONLY the value or WRONG_DOCS.
	`),
	}

	contents := []*genai.Content{
		genai.NewContentFromParts(parts, genai.RoleUser),
	}

	result, err := client.Models.GenerateContent(
		ctx,
		"gemini-2.5-flash",
		contents,
		nil,
	)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	text := strings.TrimSpace(result.Text())
	upper := strings.ToUpper(text)

	if upper == "WRONG_DOCS" {
		w.Write([]byte(`{"status":"error","message":"wrong docs"}`))
		return
	}

	// Validate salary format: VERY STRONG CHECK
	salaryRegex := regexp.MustCompile(`(?i)(₹?\s?\d{1,3}(,\d{3})*(\.\d+)?|\d{4,})`)
	match := salaryRegex.FindString(text)

	if match == "" {
		w.Write([]byte(`{"status":"error","message":"wrong docs"}`))
		return
	}

	cleanedSalary := strings.TrimSpace(match)

	resp := fmt.Sprintf(`{"status":"success","salary":"%s"}`, cleanedSalary)
	w.Write([]byte(resp))
}


func preApprovedLimit(c int64, s int64, e int64) float64 {
	csf := float64(c / maxScore)
	er := float64(s-e) / float64(s)
	return float64(s*10) * csf * er
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
	// http.HandleFunc("/credit-score", h.fetchCreditScore)
	http.HandleFunc("/loan", h.ApplyLoanHandler)
	http.HandleFunc("/upload-salary", UploadSalarySlipHandler)

	fmt.Printf("server running on port: %v\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("server start error: %v", err)
		return
	}
}
