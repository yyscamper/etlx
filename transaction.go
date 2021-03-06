package etlx

import (
	"fmt"

	"github.com/xingwangc/etlx/driver"
)

const (
	EXTRACT_DONE_SIGNAL   = "extract done"
	TRANSFORM_DONE_SIGNAL = "transform done"
)

type DataSource struct {
	//extract type
	phase string
	//subtype of the Extract transaction, e.g. txt, excel, csv, posgtres, mssql
	name string
	//data source string to extract, for file it should be file name, for sql it should be the dscp
	dataSource string
}

// Generally, 1 Transaction is consist with 3 steps: extracting -> transforming -> loading.
// For SQL、Nosql process, 1 transaction also could be handled in batch.
type Transaction struct {
	offset int64
	limit  int64

	//drivers for the transtraction
	extractDriver   driver.ExtractDriver
	transformDriver driver.TransformDriver
	loadDriver      driver.LoadDriver

	//Data source name for each phases of the transaction.
	//Different businesses may have different layout of the dsn.
	//Mostly this infor should be transfered to the driver.
	extractDsn   DataSource
	transformDsn DataSource
	loadDsn      DataSource

	//handlers returned by each driver to extract, transform and load data.
	extractHandler   driver.Extract
	transformHandler driver.Transform
	loadHandler      driver.Load

	//Interface to access the extracting results.
	//When extracting phashe is completing, this will be transfered to transforming handler.
	extractCh      chan string
	extractResults driver.Rows

	//Interface to access the transforming results.
	//When transforming phase complete, this will be transfered to loading phase
	transformCh      chan string
	transformResults driver.Results

	//Interface to access the loading results if the results is stored in some temporayi
	//storage.
	//This only could the be used if there are some transactions depends on the results
	//of this transaction.
	loadCh      chan string
	loadResults driver.Results
}

//Open init an transaction based on the name of extract, transfrom and load driver.
func Open(eName, tName, lName string) (*Transaction, error) {
	driverE, ok := drivers.Extract[eName]
	if !ok {
		return nil, fmt.Errorf("etlx: Do not find the Extract driver for name:%s", eName)
	}
	driverT, ok := drivers.Transform[tName]
	if !ok {
		return nil, fmt.Errorf("etlx: Do not find the Transform driver for name:%s", tName)
	}
	driverL, ok := drivers.Load[lName]
	if !ok {
		return nil, fmt.Errorf("etlx: Do not find the Load driver for name:%s", lName)
	}

	tsact := &Transaction{
		extractDriver:   driverE,
		transformDriver: driverT,
		loadDriver:      driverL,
	}

	tsact.extractCh = make(chan string)
	tsact.transformCh = make(chan string)
	tsact.limit = 0 //set default batch size to 0
	tsact.offset = 0

	return tsact, nil
}

//ExtractOpen init the extract driver and get the extract handler from driver.
func (t *Transaction) ExtractOpen(etype, name, dataSource string) error {
	if name == "" || dataSource == "" {
		return fmt.Errorf("Should provide extract name and datasource to init Extract")
	}

	t.extractDsn.phase = etype
	t.extractDsn.name = name
	t.extractDsn.dataSource = dataSource

	handler, err := t.extractDriver.Open(name, dataSource)
	t.extractHandler = handler
	return err
}

//TransformOpen init the transform driver and get the transform handler from driver.
func (t *Transaction) TransformOpen(ttype, name, dataSource string) error {
	t.transformDsn.phase = ttype
	t.transformDsn.name = name
	t.transformDsn.dataSource = dataSource

	handler, err := t.transformDriver.Open(name, dataSource)
	t.transformHandler = handler
	return err
}

//LoadOpen init the load driver and get the load handler from driver.
func (t *Transaction) LoadOpen(ltype, name, dataSource string) error {
	t.loadDsn.phase = ltype
	t.loadDsn.name = name
	t.loadDsn.dataSource = dataSource

	handler, err := t.loadDriver.Open(name, dataSource)
	t.loadHandler = handler
	return err
}

func (t *Transaction) extract(args []driver.Command) error {
	cmd, err := t.extractHandler.Command(args)
	if err != nil {
		fmt.Println("Extract Cmd error:", err)
		return err
	}
	fmt.Println("Extract Command:", cmd)
	results, err := t.extractHandler.Query(cmd)
	if err != nil {
		return err
	}

	t.extractResults = results
	//	t.extractCh <- EXTRACT_DONE_SIGNAL

	return nil
}

func (t *Transaction) transform(args []driver.Command) error {
	cmd, err := t.transformHandler.Command(args)
	if err != nil {
		return err
	}

	//	sig := <-t.extractCh
	//	fmt.Println("extract signal:", sig)
	//	if sig != EXTRACT_DONE_SIGNAL {
	//		return fmt.Errorf("Got the wrong signal from extract %v\n", sig)
	//	}

	results, err := t.transformHandler.Exec(t.extractResults, cmd)
	if err != nil {
		return err
	}

	t.transformResults = results
	//	t.transformCh <- TRANSFORM_DONE_SIGNAL

	return nil
}

func (t *Transaction) load(args []driver.Command) error {
	cmd, err := t.loadHandler.Command(args)
	if err != nil {
		return err
	}

	//	sig := <-t.transformCh
	//	fmt.Println("transform signal:", sig)
	//	if sig != TRANSFORM_DONE_SIGNAL {
	//		return fmt.Errorf("Got the wrong signal from transform %v\n", sig)
	//	}
	return t.loadHandler.Load(t.transformResults, cmd)
}

func (t *Transaction) Exec(extArgs []driver.Command, transArgs []driver.Command, loadArgs []driver.Command) error {
	fmt.Println("extract cmd", extArgs)
	fmt.Println("transform cmd", transArgs)
	fmt.Println("load cmd", loadArgs)
	err := t.extract(extArgs)
	if err != nil {
		return err
	}
	err = t.transform(transArgs)
	if err != nil {
		return err
	}
	err = t.load(loadArgs)
	if err != nil {
		return err
	}
	//go t.extract(extArgs)
	//go t.transform(transArgs)
	//go t.load(loadArgs)

	return nil
}

func (t *Transaction) SetBatchSize(batch int64) {
	if t.limit == 0 {
		t.limit = batch
	} else {
		t.offset += t.limit
		t.limit = batch
	}
	t.extractHandler.SetBatch(t.limit, t.offset)
}

func (t *Transaction) updateOffset(offset int64) {
	t.offset = offset
}

func (t *Transaction) extractClose() error {
	return t.extractHandler.Close()
}

func (t *Transaction) transformClose() error {
	return t.transformHandler.Close()
}

func (t *Transaction) loadClose() error {
	return t.loadHandler.Close()
}

func (t *Transaction) Close() []error {
	errSlice := []error{}

	err := t.extractClose()
	errSlice = append(errSlice, err)

	err = t.transformClose()
	errSlice = append(errSlice, err)

	err = t.loadClose()
	errSlice = append(errSlice, err)

	return errSlice
}
