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
	"github.com/jung-kurt/gofpdf"
	"google.golang.org/genai"
)

var (
	PORT        = ":4000"
	minScore    = int64(700)
	maxScore    = int64(900)
	suffPath    = "./uploads/"
	currentUser = 12
)

type LoanStage1Response struct {
	LoanId      int     `json:"loan_id"`
	Name        string  `json:"full_name"`
	LoanAmount  int     `json:"loan_amount"`
	Duration    int     `json:"duration_months"`
	MonthlyEMI  float64 `json:"monthly_emi"`
	LimitAmount int     `json:"limit_amount"`
	Status      string  `json:"status"`
	Message     string  `json:"message"`
}

type Stage2Response struct {
	LoanAmount int     `json:"loan_amount"`
	MonthlyEMI float64 `json:"monthly_emi"`
	Salary     int     `json:"salary"`
	Status     string  `json:"status"`
	Message    string  `json:"message"`
}

type Customer struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	CreditScore int    `json:"creditScore"`
	Salary      int    `json:"salary"`
	ExistingEmi int    `json:"existing_emi"`
}

type SanctionData struct {
	CustomerName string
	LoanAmount   float64
	Tenure       int
	// InterestRate  float64
	EMI float64
	// SanctionLimit float64
}

func createSanctionData(n string, l float64, t int, e float64) *SanctionData {
	return &SanctionData{
		CustomerName: n,
		LoanAmount:   l,
		Tenure:       t,
		// InterestRate: i,
		EMI: e,
		// SanctionLimit: sl,
	}
}

type Handler struct {
	db *sql.DB
}

func GenerateSanctionLetter(w http.ResponseWriter, resp *LoanStage1Response) error {
	s := createSanctionData(resp.Name, float64(resp.LoanAmount), resp.Duration, resp.MonthlyEMI)
	return GenerateSanctionPDF(*s, w)
}

func GenerateSanctionPDF(data SanctionData, w http.ResponseWriter) error {
	w.Header().Set("Content-Disposition", "attachment; filename=\"sanction_letter.pdf\"")
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)

	letter := fmt.Sprintf(`
		Dear %s,

		Congratulations! Your personal loan request has been approved.

		Loan Details:
		- Loan Amount: %.2f
		- Tenure: %d months
		- EMI: %.2f


		Regards,
		Team Potato
	`, data.CustomerName, data.LoanAmount, data.Tenure, data.EMI)

	pdf.MultiCell(0, 7, letter, "", "L", false)

	return pdf.Output(w)
}

func (h *Handler) ApplyLoanHandler(w http.ResponseWriter, r *http.Request) {
	userId := currentUser // TODO: has to be dynamic, using middleware (auth)

	// fetch from frotnend i.e forms
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

	var (
		name         string
		credit_score int
		salary       int
		existing_emi int
	)

	// fetch customer deatils i.e name, salary, credit score, existing EMI's.
	query := "SELECT full_name, credit_score, salary, existing_emi FROM users WHERE id = ?"
	if err := h.db.QueryRow(query, userId).Scan(&name, &credit_score, &salary, &existing_emi); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	user := Customer{
		ID:          userId,
		Name:        name,
		CreditScore: credit_score,
		Salary:      salary,
		ExistingEmi: existing_emi,
	}

	// TODO: calculate emi with intrest
	// 6m -> 0%, 12m -> 12.34%, 18m -> 12.67%, 24m -> 13%
	monthlyEMI := float64(loan) / float64(duration)

	limitAmount, err := h.fetchLimitAmount(&user)
	fmt.Println(limitAmount)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	resp := LoanStage1Response{
		Name:        user.Name,
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
		n, err := writeLoanToDB(h.db, &resp, userId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp.LoanId = int(n)
		if err := GenerateSanctionLetter(w, &resp); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	// document req  i.e L < loan ≤ 2L
	case int(limitAmount) < int(loan) && int(loan) < int(2*limitAmount):
		w.Header().Set("Content-Type", "application/json")
		resp.Status = "salary_verification"
		resp.Message = "please upload salary slip for further verification"
		n, err := writeLoanToDB(h.db, &resp, userId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp.LoanId = int(n)
		json.NewEncoder(w).Encode(resp)
		return
	// reject i.e loan > 2L
	case int(loan) > int(2*limitAmount):
		w.Header().Set("Content-Type", "application/json")
		resp.Status = "rejected"
		resp.Message = "requested loan exceeds maximum eligibility"
		n, err := writeLoanToDB(h.db, &resp, userId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp.LoanId = int(n)
		json.NewEncoder(w).Encode(resp)
		return
	default:
		w.Header().Set("Content-Type", "application/json")
		resp.Status = "rejected"
		resp.Message = "unable to determine eligibility."
		n, err := writeLoanToDB(h.db, &resp, userId)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp.LoanId = int(n)
		json.NewEncoder(w).Encode(resp)
	}
}

// TODO: transaction db calls
func writeLoanToDB(db *sql.DB, resp *LoanStage1Response, userId int) (int64, error) {
	query := "INSERT INTO loans (user_id, amount, months, monthly_emi, status, limit_amount) VALUES (?,?,?,?,?,?)"
	r, err := db.Exec(query, userId, resp.LoanAmount, resp.Duration, resp.MonthlyEMI, resp.Status, resp.LimitAmount)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// TODO: mine type, only pdf required
func (h *Handler) UploadSalarySlipHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userId := currentUser

	// Parse multipart form data, limit to 10MB
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	//get unique loan ID
	i := r.FormValue("loan_id")
	loanID, err := strconv.Atoi(i)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
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
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	fullPath := suffPath + strings.Join(strings.Split(handler.Filename, " "), "")
	dst, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file content to the new file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	b, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	ssalary := ocrFile(w, b)
	slipSalary, err := strconv.Atoi(ssalary)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Printf("SalarySlip Salary: %v\n", slipSalary)

	var (
		name        string
		salary      int
		existingEmi float64
		loanAmount  float64
		months      int
		monthlyEmi  float64
		status      string
		limitAmount float64
	)

	// fetch salary from users
	if err := h.db.QueryRow("SELECT full_name, salary, existing_emi FROM users WHERE id = ?", userId).Scan(&name, &salary, &existingEmi); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// fetch loan details from loans
	query := "SELECT amount, months, monthly_emi, status, limit_amount FROM loans WHERE loan_id = ?"
	if err := h.db.QueryRow(query, loanID).Scan(&loanAmount, &months, &monthlyEmi, &status, &limitAmount); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if status == "rejected" {
		http.Error(w, `{"error":'loan with this loanID is already is rejected.'}`, http.StatusBadRequest)
		return
	}
	if status != "salary_verification" {
		http.Error(w, `{"error":'wrong status'}`, http.StatusBadRequest)
		return
	}

	if slipSalary != salary {
		http.Error(w, `{"error":'salary slip salary doesn't matches your profile salary, please update profile salary and then retry for loan.'}`, http.StatusBadRequest)
		return
	}

	resp := LoanStage1Response{
		LoanId:      loanID,
		Name:        name,
		LoanAmount:  int(loanAmount),
		Duration:    months,
		MonthlyEMI:  monthlyEmi,
		LimitAmount: int(limitAmount),
	}

	available := float64(salary) - existingEmi
	maxAllowedNewEmi := available / 2
	check := monthlyEmi <= maxAllowedNewEmi

	fmt.Println(check)
	//condition check
	if check {
		resp.Status = "approved"
		resp.Message = "instant approval granted"
		// update db salary_verification -> approved
		query := `UPDATE loans SET status = ? WHERE loan_id = ?`
		_, err := h.db.Exec(query, resp.Status, loanID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		if err := GenerateSanctionLetter(w, &resp); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		return
	}

	resp.Status = "rejected"
	resp.Message = "requested loan exceeds maximum eligibility"

	query = `UPDATE loans SET status = ? WHERE loan_id = ?`
	_, err = h.db.Exec(query, resp.Status, loanID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) fetchLimitAmount(user *Customer) (int, error) {
	// check for threshold score
	if int64(user.CreditScore) < minScore {
		log.Printf("cannot procede further, low credit score.")
		return -1, fmt.Errorf("low credit score")
	}

	// calculate pre-approved limit
	limitAmount := preApprovedLimit(int64(user.CreditScore), int64(user.Salary), int64(user.ExistingEmi))
	return int(limitAmount), nil
}

func ocrFile(w http.ResponseWriter, b []byte) string {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return ""
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
		return ""
	}

	text := strings.TrimSpace(result.Text())
	upper := strings.ToUpper(text)

	if upper == "WRONG_DOCS" {
		w.Write([]byte(`{"status":"error","message":"wrong docs"}`))
		return ""
	}

	// Validate salary format: VERY STRONG CHECK
	salaryRegex := regexp.MustCompile(`(?i)(₹?\s?\d{1,3}(,\d{3})*(\.\d+)?|\d{4,})`)
	match := salaryRegex.FindString(text)

	if match == "" {
		w.Write([]byte(`{"status":"error","message":"wrong docs"}`))
		return ""
	}

	cleanedSalary := strings.Join(strings.Split(strings.TrimSpace(match), ","), "")

	// resp := fmt.Sprintf(`{"status":"success","salary":"%s"}`, cleanedSalary)
	// w.Write([]byte(resp))
	return cleanedSalary
}

func preApprovedLimit(cs int64, s int64, e int64) float64 {
	csf := float64(cs) / float64(maxScore)
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
	http.HandleFunc("/upload-salary", h.UploadSalarySlipHandler)
	// http.HandleFunc("/sanction-letter", h.GenerateSanctionLetter)

	fmt.Printf("server running on port: %v\n", PORT)
	if err := http.ListenAndServe(PORT, nil); err != nil {
		fmt.Printf("server start error: %v", err)
		return
	}
}
