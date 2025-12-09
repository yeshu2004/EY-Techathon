CREATE TABLE users ( 
    id int primary key auto_increment, 
    full_name VARCHAR(150) NOT NULL, 
    email VARCHAR(150) UNIQUE NOT NULL, 
    phone VARCHAR(15) UNIQUE NOT NULL, 
    credit_score INT, 
    salary INT, 
    existing_emi INT, 
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP 
)


INSERT INTO users (full_name, email, phone, credit_score, salary, existing_emi)
VALUES
('Aarav Sharma', 'aarav.sharma@example.com', '9876543201', 812, 65000, 5000),
('Priya Verma', 'priya.verma@example.com', '9876543202', 725, 42000, 0),
('Rohan Gupta', 'rohan.gupta@example.com', '9876543203', 680, 38000, 3000),
('Sneha Kulkarni', 'sneha.kulkarni@example.com', '9876543204', 790, 52000, 0),
('Vikram Singh', 'vikram.singh@example.com', '9876543205', 845, 90000, 12000),
('Nisha Patel', 'nisha.patel@example.com', '9876543206', 710, 45000, 5000),
('Karan Mehta', 'karan.mehta@example.com', '9876543207', 640, 30000, 2000),
('Ananya Iyer', 'ananya.iyer@example.com', '9876543208', 780, 55000, 0),
('Siddharth Rao', 'siddharth.rao@example.com', '9876543209', 805, 72000, 10000),
('Meera Joshi', 'meera.joshi@example.com', '9876543210', 695, 41000, 0);
('Mukul Sharma', 'mukul.sharma@example.com', '9076543201', 720, 80000, 20000);
